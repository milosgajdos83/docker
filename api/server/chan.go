package server

import (
	"io"
	"net"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/engine"
	"github.com/docker/libchan"
	"github.com/docker/libchan/spdy"
)

type ChanServer struct {
	l   net.Listener
	eng *engine.Engine
}

func (s *ChanServer) Serve() error {
	tl, err := spdy.NewTransportListener(s.l, spdy.NoAuthenticator)
	if err != nil {
		return err
	}

	for {
		t, err := tl.AcceptTransport()
		if err != nil {
			log.Error(err)
			continue
		}

		go func() {
			for {
				receiver, err := t.WaitReceiveChannel()
				if err != nil {
					log.Error(err)
					break
				}
				if err := s.handleConn(receiver); err != nil {
					log.Error(err)
					break
				}
			}
		}()
	}
	return nil
}

func (s *ChanServer) Close() error {
	return s.l.Close()
}

func setupChanUnix(addr string, job *engine.Job) (*ChanServer, error) {
	var (
		l   net.Listener
		err error
		eng = job.Eng
	)

	mask, err := cleanupUnix(addr)
	if err != nil {
		return nil, err
	}
	l, err = newListener("unix", addr, job.GetenvBool("BufferRequests"))
	if err != nil {
		return nil, err
	}
	syscall.Umask(mask)

	if err := setSocketGroup(addr, job.Getenv("SocketGroup")); err != nil {
		return nil, err
	}

	return &ChanServer{l, eng}, nil
}

func setupChanTcp(addr string, job *engine.Job) (*ChanServer, error) {
	var (
		l       net.Listener
		err     error
		tlsCert = job.Getenv("TlsCert")
		tlsKey  = job.Getenv("TlsKey")
		eng     = job.Eng
	)

	l, err = newListener("tcp", addr, job.GetenvBool("BufferRequests"))
	if err != nil {
		return nil, err
	}

	var ca string
	if job.GetenvBool("TlsVerify") {
		ca = job.Getenv("TlsCa")
	}
	l, err = setupTls(tlsCert, tlsKey, ca, l)
	if err != nil {
		return nil, err
	}
	return &ChanServer{l, eng}, err
}

type ChanMessage struct {
	Ret  libchan.Sender
	In   io.ReadCloser
	Job  string
	Name string
	Env  map[string]interface{}
}

func (s *ChanServer) handleConn(receiver libchan.Receiver) error {
	var (
		msg = &ChanMessage{}
	)

	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	if err := receiver.Receive(msg); err != nil {
		return err
	}

	job := s.eng.Job(msg.Job, msg.Name)
	job.ImportEnv(msg.Env)

	job.Stdout.Add(outW)
	job.Stderr.Add(errW)
	if msg.In != nil {
		job.Stdin.Add(msg.In)
	}

	chJobErr := make(chan error)
	go func() {
		chJobErr <- job.Run()
	}()

	type retMsg struct {
		Errors io.ReadCloser
		Msg    io.ReadCloser
	}

	if err := msg.Ret.Send(&retMsg{errR, outR}); err != nil {
		return err
	}

	return <-chJobErr
}
