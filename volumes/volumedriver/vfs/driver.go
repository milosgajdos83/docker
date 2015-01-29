package vfs

import (
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/volumes/volumedriver"
	"github.com/docker/libcontainer/label"
)

type Driver struct {
	ID         string
	MountLabel string
	Path       string
}

const DriverName = "vfs"

func init() {
	volumedriver.Register(DriverName, Init)
}

func (d *Driver) DriverName() string {
	return DriverName
}

func Init(options []string) (volumedriver.Driver, error) {
	var id string
	opts := volumedriver.DriverOpts(options)

	path, err := opts.Get("path")
	if err != nil || path == "" {
		home, err := opts.Get("home")
		if err != nil {
			return nil, err
		}

		id, err := opts.Get("id")
		if err != nil {
			return nil, err
		}

		path = filepath.Join(filepath.Clean(home), "dir", filepath.Base(id))
	}

	if cleanPath, err := filepath.EvalSymlinks(path); err == nil {
		path = cleanPath
	}

	if _, err := os.Stat(path); err != nil && !os.IsNotExist(err) {
		return nil, volumedriver.DataExist
	}

	d := &Driver{
		ID:   id,
		Path: path,
	}
	return d, nil
}

func (d *Driver) String() string {
	return d.Path
}

func (d *Driver) Create() error {
	_, err := os.Stat(d.Path)
	if err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(d.Path, 0700); err != nil {
			return err
		}
	}

	if _, mountLabel, err := label.InitLabels([]string{"level:s0"}); err == nil {
		label.Relabel(d.Path, mountLabel, "")
	}

	return nil
}

func (d *Driver) Remove() error {
	if _, err := os.Stat(d.Path); err != nil {
		return err
	}
	return os.RemoveAll(d.Path)
}

func (d *Driver) Mount(dst, mode string) error {
	return mount.Mount(d.Path, dst, "bind", "rbind,rw")
}

func (d *Driver) Unmount(dst string) error {
	return mount.ForceUnmount(dst)
}

func (d *Driver) Export(resource string) (io.ReadCloser, error) {
	resource, err := d.getResourcePath(resource)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(resource)
	if err != nil {
		return nil, err
	}

	var filter []string
	if !stat.IsDir() {
		d, f := path.Split(resource)
		resource = d
		filter = []string{f}
	} else {
		filter = []string{path.Base(resource)}
		resource = path.Dir(resource)
	}

	return archive.TarWithOptions(resource, &archive.TarOptions{
		Compression:  archive.Uncompressed,
		IncludeFiles: filter,
	})
}

func (d *Driver) getResourcePath(resource string) (string, error) {
	cleanPath := filepath.Join("/", resource)
	return symlink.FollowSymlinkInScope(filepath.Join(d.Path, cleanPath), d.Path)
}
