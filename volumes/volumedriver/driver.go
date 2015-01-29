package volumedriver

import (
	"errors"
	"io"
)

var drivers map[string]*RegisteredDriver

var DriverNotExist = errors.New("volume driver does not exist")
var DataExist = errors.New("data exists")

type Driver interface {
	// DriverName returns the driver's name
	DriverName() string
	// Create creates a new volume with the driver's config.
	Create() error
	// Remove attempts to remove the volume
	Remove() error
	// Mount mounts the volume to the specified destination
	// mode should be an fstab style mode
	Mount(dst, mode string) error
	// Unmount unmounts the volume from the specified path
	Unmount(dst string) error
	// String returns a string representation of this driver instance
	String() string
	// Export returns an IO stream containing the volume data from the provided resource path
	// The resource can be ""
	Export(resource string) (io.ReadCloser, error)
}

type InitFunc func(options []string) (Driver, error)

type RegisteredDriver struct {
	New InitFunc
}

func init() {
	drivers = make(map[string]*RegisteredDriver)
}

func Register(name string, initFunc InitFunc) {
	drivers[name] = &RegisteredDriver{New: initFunc}
}

func NewDriver(name string, opts []string) (Driver, error) {
	driver, exists := drivers[name]
	if !exists {
		return nil, DriverNotExist
	}

	return driver.New(opts)
}
