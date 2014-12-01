package plugin

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"

	"github.com/docker/libchan/spdy"
)

type ExecConfiguration struct {
	Command string
	Args    []string
}

type ExecServer struct {
	*Server
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	config *ExecConfiguration
}

func getFileTransport(fd uintptr, name string) (*spdy.Transport, error) {
	f := os.NewFile(fd, name)
	l, err := net.FileConn(f)
	if err != nil {
		return nil, err
	}
	return spdy.NewClientTransport(l)
}

func NewExecPlugin() (*Plugin, error) {
	tIn, err := getFileTransport(uintptr(3), "chanin")
	if err != nil {
		return nil, err
	}
	chanIn, err := tIn.WaitReceiveChannel()
	if err != nil {
		return nil, err
	}

	tOut, err := getFileTransport(uintptr(4), "chanout")
	if err != nil {
		return nil, err
	}
	chanOut, err := tOut.NewSendChannel()
	if err != nil {
		return nil, err
	}

	return &Plugin{
		In:  chanIn,
		Out: chanOut,
	}, nil
}

func newFileSocketPairs() (*os.File, *os.File, *os.File, *os.File, error) {
	inFd, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	outFd, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return os.NewFile(uintptr(inFd[0]), "chanInChild"),
		os.NewFile(uintptr(inFd[1]), "chanInParent"),
		os.NewFile(uintptr(outFd[0]), "chanOutChild"),
		os.NewFile(uintptr(outFd[1]), "chanOutParent"),
		nil
}

func NewExecServer(config *ExecConfiguration) *ExecServer {
	return &ExecServer{Server: &Server{}, config: config}
}

func (p *ExecServer) Start() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.cmd != nil {
		return fmt.Errorf("Plugin already loaded")
	}

	outChild, outParent, inChild, inParent, err := newFileSocketPairs()
	if err != nil {
		return err
	}
	defer inChild.Close()
	defer outChild.Close()

	outConn, err := net.FileConn(outParent)
	if err != nil {
		inParent.Close()
		return err
	}

	inConn, err := net.FileConn(inParent)
	if err != nil {
		outParent.Close()
		return err
	}

	cmd := exec.Command(p.config.Command, p.config.Args...)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{outChild, inChild}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	if err := cmd.Start(); err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	tOut, err := spdy.NewServerTransport(outConn)
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}
	out, err := tOut.NewSendChannel()
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	tIn, err := spdy.NewServerTransport(inConn)
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}
	in, err := tIn.WaitReceiveChannel()
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	p.In = in
	p.Out = out
	p.stdin = stdin
	p.cmd = cmd

	return nil
}

func (p *ExecServer) Stop() error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.cmd == nil {
		return fmt.Errorf("Not started")
	}

	if p.stdin != nil {
		if err := p.stdin.Close(); err != nil {
			return err
		}
	}
	return p.cmd.Wait()
}
