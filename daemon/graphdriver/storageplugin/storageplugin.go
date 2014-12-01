package storageplugin

import (
	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/parsers"
	"github.com/docker/docker/plugin"
	plugindriver "github.com/docker/docker/plugin/graphdriver"
	"github.com/docker/libchan"
)

type Driver struct {
	server  *plugin.ExecServer
	options []string
	name    string
	home    string
}

func init() {
	graphdriver.Register("plugin", Init)
}

func Init(home string, options []string) (graphdriver.Driver, error) {
	var cmd string
	for _, opt := range options {
		key, val, err := parsers.ParseKeyValueOpt(opt)
		if err != nil {
			return nil, err
		}
		if key == "cmd" {
			cmd = val
			break
		}
	}

	options = append(options, fmt.Sprintf("home=%s", home))
	server := plugin.NewExecServer(
		&plugin.ExecConfiguration{
			Command: cmd,
			Args:    options,
		},
	)
	d := &Driver{
		server:  server,
		options: options,
	}
	return graphdriver.NaiveDiffDriver(d), d.initDriver()
}

func (d *Driver) initDriver() error {
	return d.server.Start()
}

func (d *Driver) String() string {
	rec, err := d.call(plugindriver.Message{Func: "String"})
	if err != nil {
		return ""
	}
	var name plugindriver.StringResp
	if err := rec.Receive(&name); err != nil {
		return ""
	}
	return name.String
}

func (d *Driver) Create(id, parent string) error {
	data := plugindriver.Message{Func: "Create", Create: plugindriver.CreateReq{Id: id, Parent: parent}}
	rec, err := d.call(data)
	if err != nil {
		return err
	}
	var recError = &plugindriver.ErrResp{}
	if err := rec.Receive(recError); err != nil {
		return fmt.Errorf("Error receiving response: %v", err)
	}
	return recError.Err
}

func (d *Driver) Remove(id string) error {
	rec, err := d.call(plugindriver.Message{Func: "Remove", Remove: id})
	if err != nil {
		return err
	}
	var resp = &plugindriver.ErrResp{}
	if err := rec.Receive(resp); err != nil {
		return err
	}
	return resp.Err
}

func (d *Driver) Get(id, mountLabel string) (string, error) {
	data := plugindriver.GetReq{Id: id, MountLabel: mountLabel}
	rec, err := d.call(plugindriver.Message{Func: "Get", Get: data})
	if err != nil {
		return "", err
	}

	var resp struct {
		Path string
		Err  error
	}
	if err := rec.Receive(&resp); err != nil {
		return "", err
	}
	return resp.Path, resp.Err
}

func (d *Driver) Put(id string) {
	rec, err := d.call(plugindriver.Message{Func: "Put", Put: id})
	if err != nil {
		log.Errorf("Error calling Put on storage driver: %v", err)
		return
	}
	if err := rec.Receive(&plugindriver.EmptyResp{}); err != nil {
		log.Errorf("Error receiving Put on storage driver: %v", err)
	}
	return
}

func (d *Driver) Exists(id string) bool {
	rec, err := d.call(plugindriver.Message{Func: "Exists", Exists: id})
	if err != nil {
		return false
	}
	var resp plugindriver.BoolResp
	if err := rec.Receive(&resp); err != nil {
		return false
	}
	return resp.Bool
}

func (d *Driver) Status() [][2]string {
	rec, err := d.call(plugindriver.Message{Func: "Status"})
	if err != nil {
		log.Errorf("Error getting storage driver status: %v", err)
		return nil
	}
	var resp plugindriver.StatusResp
	if err := rec.Receive(&resp); err != nil {
		log.Errorf("Error getting storage driver status: %v", err)
	}
	return resp.Status
}

func (d *Driver) Cleanup() error {
	rec, err := d.call(plugindriver.Message{Func: "Cleanup"})
	if err != nil {
		return err
	}
	var errResp plugindriver.ErrResp
	if err := rec.Receive(&errResp); err != nil {
		return err
	}
	return errResp.Err
}

func (d *Driver) Diff(id, parent string) (archive.Archive, error) {
	data := plugindriver.DiffReq{Id: id, Parent: parent}
	rec, err := d.call(plugindriver.Message{Func: "Diff", Diff: data})
	if err != nil {
		return nil, err
	}
	var resp plugindriver.DiffResp
	if err := rec.Receive(&resp); err != nil {
		return nil, err
	}
	return resp.Archive, resp.Err
}

func (d *Driver) Changes(id, parent string) ([]archive.Change, error) {
	data := plugindriver.DiffReq{Id: id, Parent: parent}
	rec, err := d.call(plugindriver.Message{Func: "Changes", Changes: data})
	if err != nil {
		return nil, err
	}
	var resp plugindriver.ChangesResp
	if err := rec.Receive(&resp); err != nil {
		return nil, err
	}
	return resp.Changes, resp.Err
}

func (d *Driver) ApplyDiff(id, parent string, diff archive.ArchiveReader) (int64, error) {
	data := plugindriver.ApplyDiffReq{Id: id, Parent: parent, Diff: diff}
	rec, err := d.call(plugindriver.Message{Func: "ApplyDiff", ApplyDiff: data})
	if err != nil {
		return int64(0), err
	}
	var resp plugindriver.ApplyDiffResp
	if err := rec.Receive(&resp); err != nil {
		return int64(0), err
	}
	return resp.Bytes, resp.Err
}

func (d *Driver) DiffSize(id, parent string) (int64, error) {
	data := plugindriver.DiffReq{Id: id, Parent: parent}
	rec, err := d.call(plugindriver.Message{Func: "ApplyDiff", DiffSize: data})
	if err != nil {
		return int64(0), err
	}
	var resp plugindriver.ApplyDiffResp
	if err := rec.Receive(&resp); err != nil {
		return int64(0), err
	}
	return resp.Bytes, resp.Err
}

type NopReader struct{}

func (r *NopReader) Read(p []byte) (int, error) {
	return 0, nil
}

func (d *Driver) call(msg plugindriver.Message) (libchan.Receiver, error) {
	if d.server == nil {
		return nil, fmt.Errorf("plugin not registered")
	}

	return d.server.In, d.server.Out.Send(msg)
}
