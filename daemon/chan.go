package daemon

import (
	"fmt"
	"io"
	"net"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/engine"
	"github.com/docker/libchan"
	"github.com/docker/libchan/spdy"
)

type chanRepo struct {
	chans map[string]net.Listener
	lock  sync.Mutex
}

func (r *chanRepo) find(id string) net.Listener {
	return r.chans[id]
}

func (r *chanRepo) Find(id string) net.Listener {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.find(id)
}

func (r *chanRepo) Remove(id string) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	l := r.find(id)
	if l == nil {
		return nil
	}

	log.Debugf("Removing channel registration for %s", id)
	delete(r.chans, id)
	return l.Close()
}

func (r *chanRepo) Add(id string, l net.Listener) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if l := r.find(id); l != nil {
		return fmt.Errorf("chan already exists for %s")
	}

	r.chans[id] = l
	return nil
}

func newChanRepo() *chanRepo {
	chans := make(map[string]net.Listener)
	return &chanRepo{
		chans: chans,
	}
}

func ServeChan(path string, eng *engine.Engine) (net.Listener, error) {
	var (
		l   net.Listener
		err error
	)

	l, err = net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	tl, err := spdy.NewTransportListener(l, spdy.NoAuthenticator)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			t, err := tl.AcceptTransport()
			if err != nil {
				log.Error(err)
				break
			}
			go func() {
				for {
					receiver, err := t.WaitReceiveChannel()
					if err != nil {
						log.Error(err)
						break
					}
					if err := handleChanConn(receiver, eng); err != nil {
						log.Error(err)
						break
					}
				}
			}()
		}
	}()
	return l, nil
}

type ChanMessage struct {
	Ret  libchan.Sender
	Job  string
	Data map[string]string
}

func handleChanConn(receiver libchan.Receiver, eng *engine.Engine) error {
	var (
		msg = &ChanMessage{}
	)

	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	if err := receiver.Receive(msg); err != nil {
		return err
	}

	job := eng.Job(msg.Job, msg.Data["name"])
	for k, v := range msg.Data {
		job.Setenv(k, v)
	}

	job.Stdout.Add(outW)
	job.Stderr.Add(errW)

	go func() {
		job.Run()
	}()

	type retMsg struct {
		Errors io.ReadCloser
		Msg    io.ReadCloser
	}

	if err := msg.Ret.Send(&retMsg{errR, outR}); err != nil {
		return err
	}

	return nil
}
