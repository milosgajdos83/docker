package plugin

import (
	"sync"

	"github.com/docker/libchan"
)

type Message struct {
	Data interface{}
	Func string
}

type Plugin struct {
	In  libchan.Receiver
	Out libchan.Sender
}

type Server struct {
	In   libchan.Receiver
	Out  libchan.Sender
	lock sync.Mutex
}

func (s *Server) Send(msg *Message) error {
	return s.Out.Send(msg)
}
