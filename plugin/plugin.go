package plugin

import (
	"fmt"
	"net"
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/engine"
	"github.com/docker/libchan"
	"github.com/docker/libchan/spdy"
)

type Message struct {
	Env  *engine.Env
	Func string
}

type Plugin struct {
	In     libchan.Receiver
	Out    libchan.Sender
	router map[string]PluginFunc
}

type PluginFunc func(env *engine.Env) (interface{}, error)

func getTransport(fd uintptr, name string) (*spdy.Transport, error) {
	f := os.NewFile(fd, name)
	l, err := net.FileConn(f)
	if err != nil {
		return nil, err
	}
	return spdy.NewClientTransport(l)
}

func NewPlugin(router map[string]PluginFunc) (*Plugin, error) {
	tIn, err := getTransport(uintptr(3), "chanin")
	if err != nil {
		return nil, err
	}
	log.Infof("plugin: waiting for receiver")
	chanIn, err := tIn.WaitReceiveChannel()
	if err != nil {
		return nil, err
	}

	tOut, err := getTransport(uintptr(4), "chanout")
	if err != nil {
		return nil, err
	}
	log.Infof("plugin: getting send channel")
	chanOut, err := tOut.NewSendChannel()
	if err != nil {
		return nil, err
	}

	log.Infof("plugin: created")

	return &Plugin{
		In:     chanIn,
		Out:    chanOut,
		router: router,
	}, nil
}

func (p *Plugin) Receive() error {
	msg := &Message{}
	if err := p.In.Receive(msg); err != nil {
		return err
	}
	return p.dispatch(msg)
}

func (p *Plugin) dispatch(msg *Message) error {
	if _, exists := p.router[msg.Func]; !exists {
		return fmt.Errorf("%s not supported", msg.Func)
	}

	resp, err := p.router[msg.Func](msg.Env)
	if err != nil {
		return err
	}
	return p.Send(resp)
}

func (p *Plugin) Send(i interface{}) error {
	return p.Out.Send(i)
}
