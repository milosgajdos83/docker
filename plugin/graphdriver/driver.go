package graphdriver

import (
	"fmt"

	log "github.com/Sirupsen/logrus"
	driver "github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/plugin"
	"github.com/docker/libchan"
)

type Driver struct {
	*plugin.Plugin
	driver driver.Driver
	router map[string]PluginFunc
}

type reqInit struct {
	Root    string
	Options []string
}
type respInit struct {
	Driver Driver
	Err    error
}

type StatusResp struct {
	Status [][2]string
}

type StringResp struct {
	String string
}

type CreateReq struct {
	Id     string
	Parent string
}

type GetReq struct {
	Id         string
	MountLabel string
}
type GetResp struct {
	Dir string
	Err error
}

type DiffReq struct {
	Id     string
	Parent string
}
type DiffResp struct {
	Archive archive.Archive
	Err     error
}

type ChangesResp struct {
	Changes []archive.Change
	Err     error
}

type ApplyDiffReq struct {
	Id     string
	Parent string
	Diff   archive.ArchiveReader
}
type ApplyDiffResp struct {
	Bytes int64
	Err   error
}

type InitReq struct {
	Home string
	Opts []string
}

type ErrResp struct {
	Err error
}

type BoolResp struct {
	Bool bool
}

type EmptyResp struct{}

type Message struct {
	Func      string
	Create    CreateReq
	Remove    string
	Put       string
	Get       GetReq
	Exists    string
	Diff      DiffReq
	Changes   DiffReq
	ApplyDiff ApplyDiffReq
	DiffSize  DiffReq
}

type PluginFunc func(libchan.Sender, *Message) error

func newRouter(d *Driver) map[string]PluginFunc {
	return map[string]PluginFunc{
		"Status":    d.Status,
		"Create":    d.Create,
		"Cleanup":   d.Cleanup,
		"Get":       d.Get,
		"Remove":    d.Remove,
		"Diff":      d.Diff,
		"Exists":    d.Exists,
		"Changes":   d.Changes,
		"ApplyDiff": d.ApplyDiff,
		"DiffSize":  d.DiffSize,
		"String":    d.String,
		"Put":       d.Put,
	}
}

func NewGraphDriver(driver driver.Driver) (*Driver, error) {
	var err error
	d := &Driver{driver: driver}
	d.router = newRouter(d)
	d.Plugin, err = plugin.NewExecPlugin()
	return d, err
}

func (d *Driver) Status(out libchan.Sender, msg *Message) error {
	return out.Send(StatusResp{Status: d.driver.Status()})
}

func (d *Driver) Create(out libchan.Sender, msg *Message) error {
	return out.Send(&ErrResp{Err: d.driver.Create(msg.Create.Id, msg.Create.Parent)})
}

func (d *Driver) Cleanup(out libchan.Sender, msg *Message) error {
	return out.Send(ErrResp{Err: d.driver.Cleanup()})
}

func (d *Driver) Get(out libchan.Sender, msg *Message) error {
	dir, err := d.driver.Get(msg.Get.Id, msg.Get.MountLabel)
	return out.Send(GetResp{Dir: dir, Err: err})
}

func (d *Driver) Remove(out libchan.Sender, msg *Message) error {
	return out.Send(&ErrResp{Err: d.driver.Remove(msg.Remove)})
}

func (d *Driver) Exists(out libchan.Sender, msg *Message) error {
	return out.Send(BoolResp{d.driver.Exists(msg.Exists)})
}

func (d *Driver) Diff(out libchan.Sender, msg *Message) error {
	a, err := d.driver.Diff(msg.Diff.Id, msg.Diff.Parent)
	return out.Send(DiffResp{Archive: a, Err: err})
}

func (d *Driver) Changes(out libchan.Sender, msg *Message) error {
	a, err := d.driver.Changes(msg.Changes.Id, msg.Changes.Parent)
	return out.Send(ChangesResp{Changes: a, Err: err})
}

func (d *Driver) ApplyDiff(out libchan.Sender, msg *Message) error {
	bytes, err := d.driver.ApplyDiff(msg.ApplyDiff.Id, msg.ApplyDiff.Parent, msg.ApplyDiff.Diff)
	return out.Send(ApplyDiffResp{Bytes: bytes, Err: err})
}

func (d *Driver) DiffSize(out libchan.Sender, msg *Message) error {
	bytes, err := d.driver.DiffSize(msg.DiffSize.Id, msg.DiffSize.Parent)
	return out.Send(ApplyDiffResp{Bytes: bytes, Err: err})
}

func (d *Driver) String(out libchan.Sender, msg *Message) error {
	return out.Send(StringResp{String: d.driver.String()})
}

func (d *Driver) Put(out libchan.Sender, msg *Message) error {
	d.driver.Put(msg.Put)
	return out.Send(&EmptyResp{})
}

func (p *Driver) Receive() error {
	var msg = &Message{}
	if err := p.In.Receive(msg); err != nil {
		log.Errorf("Error receiving message: %v", err)
		return err
	}
	return p.dispatch(msg)
}

func (p *Driver) dispatch(msg *Message) error {
	method, exists := p.getMethod(msg.Func)
	if !exists {
		return fmt.Errorf("%s not supported", msg.Func)
	}
	log.Infof("Dispatching message %v", msg)
	return method(p.Out, msg)
}

func (p *Driver) getMethod(name string) (PluginFunc, bool) {
	method, exists := p.router[name]
	return method, exists
}
