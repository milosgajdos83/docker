package server

import (
	"crypto/tls"
	"crypto/x509"

	"fmt"
	"io/ioutil"
	"net"

	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/docker/libcontainer/user"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/engine"
	"github.com/docker/docker/pkg/listenbuffer"

	"github.com/docker/docker/pkg/systemd"
)

var (
	activationLock chan struct{}
)

func lookupGidByName(nameOrGid string) (int, error) {
	groups, err := user.ParseGroupFilter(func(g *user.Group) bool {
		return g.Name == nameOrGid || strconv.Itoa(g.Gid) == nameOrGid
	})
	if err != nil {
		return -1, err
	}
	if groups != nil && len(groups) > 0 {
		return groups[0].Gid, nil
	}
	return -1, fmt.Errorf("Group %s not found", nameOrGid)
}

func changeGroup(addr string, nameOrGid string) error {
	gid, err := lookupGidByName(nameOrGid)
	if err != nil {
		return err
	}

	log.Debugf("%s group found. gid: %d", nameOrGid, gid)
	return os.Chown(addr, 0, gid)
}

func setupTls(cert, key, ca string, l net.Listener) (net.Listener, error) {
	tlsCert, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("Couldn't load X509 key pair (%s, %s): %s. Key encrypted?",
			cert, key, err)
	}
	tlsConfig := &tls.Config{
		NextProtos:   []string{"http/1.1"},
		Certificates: []tls.Certificate{tlsCert},
		// Avoid fallback on insecure SSL protocols
		MinVersion: tls.VersionTLS10,
	}

	if ca != "" {
		certPool := x509.NewCertPool()
		file, err := ioutil.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("Couldn't read CA certificate: %s", err)
		}
		certPool.AppendCertsFromPEM(file)
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = certPool
	}

	return tls.NewListener(l, tlsConfig), nil
}

type Server interface {
	Serve() error
	Close() error
}

func newListener(proto, addr string, bufferRequests bool) (net.Listener, error) {
	if bufferRequests {
		return listenbuffer.NewListenBuffer(proto, addr, activationLock)
	}

	return net.Listen(proto, addr)
}

func cleanupUnix(addr string) (int, error) {
	var mask = 0777
	if err := syscall.Unlink(addr); err != nil && !os.IsNotExist(err) {
		return mask, err
	}
	syscall.Umask(mask)
	return mask, nil
}

// NewServer sets up the required Server and does protocol specific checking.
func NewServer(proto, addr string, job *engine.Job) (Server, error) {
	// Basic error and sanity checking
	switch proto {
	case "fd":
		return nil, ServeFd(addr, job)
	case "tcp":
		return setupTcpHttp(addr, job)
	case "unix":
		return setupUnixHttp(addr, job)
	case "chan-unix":
		return setupChanUnix(addr, job)
	case "chan-tcp":
		return setupChanTcp(addr, job)
	default:
		return nil, fmt.Errorf("Invalid protocol format.")
	}
}

func setSocketGroup(addr, group string) error {
	if group == "" {
		return nil
	}

	if err := changeGroup(addr, group); err != nil {
		if group != "docker" {
			return err
		}
		log.Debugf("Warning: could not chgrp %s to docker: %q", addr, err)
	}

	return nil
}

// ServeApi loops through all of the protocols sent in to docker and spawns
// off a go routine to setup a serving http.Server for each.
func ServeApi(job *engine.Job) engine.Status {
	if len(job.Args) == 0 {
		return job.Errorf("usage: %s PROTO://ADDR [PROTO://ADDR ...]", job.Name)
	}
	var (
		protoAddrs = job.Args
		chErrors   = make(chan error, len(protoAddrs))
	)
	activationLock = make(chan struct{})

	for _, protoAddr := range protoAddrs {
		protoAddrParts := strings.SplitN(protoAddr, "://", 2)
		if len(protoAddrParts) != 2 {
			return job.Errorf("usage: %s PROTO://ADDR [PROTO://ADDR ...]", job.Name)
		}
		go func() {
			log.Infof("Listening for HTTP on %s (%s)", protoAddrParts[0], protoAddrParts[1])
			srv, err := NewServer(protoAddrParts[0], protoAddrParts[1], job)
			if err != nil {
				chErrors <- err
				return
			}
			chErrors <- srv.Serve()
		}()
	}

	for i := 0; i < len(protoAddrs); i++ {
		err := <-chErrors
		if err != nil {
			return job.Error(err)
		}
	}

	return engine.StatusOK
}

func AcceptConnections(job *engine.Job) engine.Status {
	// Tell the init daemon we are accepting requests
	go systemd.SdNotify("READY=1")

	// close the lock so the listeners start accepting connections
	if activationLock != nil {
		close(activationLock)
	}

	return engine.StatusOK
}
