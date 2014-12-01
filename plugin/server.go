package plugin

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/libchan"
	"github.com/docker/libchan/spdy"
)

type PluginServer struct {
	cmd    *exec.Cmd
	config *PluginConfiguration
	In     libchan.Sender
	Out    libchan.Receiver
	stdin  io.WriteCloser
	lock   sync.Mutex
}

type PluginConfiguration struct {
	Command string
	Args    []string
}

func NewPluginServer(config *PluginConfiguration) *PluginServer {
	return &PluginServer{config: config}
}

func (p *PluginServer) Start() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.cmd != nil {
		return fmt.Errorf("Plugin already loaded")
	}

	log.Infof("Creating socket pairs")
	inChild, inParent, outChild, outParent, err := newSocketPairs()
	if err != nil {
		return err
	}
	defer inChild.Close()
	defer outChild.Close()

	log.Infof("Creating In file conn")
	inConn, err := net.FileConn(inParent)
	if err != nil {
		inParent.Close()
		return err
	}

	log.Infof("Creating Out file conn")
	outConn, err := net.FileConn(outParent)
	if err != nil {
		outParent.Close()
		return err
	}

	cmd := exec.Command(p.config.Command, p.config.Args...)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{inChild, outChild}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	log.Infof("Starting cmd")
	if err := cmd.Start(); err != nil {
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
	log.Infof("New in channel")
	in, err := tIn.NewSendChannel()
	if err != nil {
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
	log.Infof("New out channel")
	out, err := tOut.WaitReceiveChannel()
	if err != nil {
		outConn.Close()
		inConn.Close()
		return err
	}

	p.In = in
	p.Out = out
	p.stdin = stdin
	p.cmd = cmd
	log.Infof("Plugin created")

	return nil
}

func (p *PluginServer) Stop() error {
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

func newSocketPairs() (*os.File, *os.File, *os.File, *os.File, error) {
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
