package host

import (
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/volumes/volumedriver"
)

const DriverName = "host"

func init() {
	volumedriver.Register(DriverName, Init)
}

type Driver struct {
	Path string
}

func Init(options []string) (volumedriver.Driver, error) {
	opts := volumedriver.DriverOpts(options)

	path, err := opts.Get("path")
	if err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if cleanPath, err := filepath.EvalSymlinks(path); err == nil {
		path = cleanPath
	}

	return &Driver{Path: path}, nil
}

func (d *Driver) DriverName() string {
	return DriverName
}

func (d *Driver) String() string {
	return d.Path
}

func (d *Driver) Create() error {
	// FIXME: This should probably check if the parent dir exists and error if it does not
	//		This would be a breaking change, however.
	_, err := os.Stat(d.Path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(d.Path, 0700); err != nil {
		return err
	}

	return nil
}

func (d *Driver) Remove() error {
	// Do not remove host dirs
	return nil
}

func (d *Driver) Mount(dst, mode string) error {
	return mount.Mount(d.Path, dst, "rbind", mode)
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
