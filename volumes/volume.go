package volumes

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/volumes/volumedriver"
)

type Volume struct {
	ID         string // ID assigned by docker
	containers map[string]struct{}
	configPath string
	*volumeConfig
	volumedriver.Driver
	Path     string // Deprecated
	Writable bool   // Deprecated
	lock     sync.Mutex
}

type volumeConfig struct {
	DriverName string
	Opts       []string
}

func (v *Volume) Containers() []string {
	v.lock.Lock()

	var containers []string
	for c := range v.containers {
		containers = append(containers, c)
	}

	v.lock.Unlock()
	return containers
}

func (v *Volume) RemoveContainer(containerId string) {
	v.lock.Lock()
	delete(v.containers, containerId)
	v.lock.Unlock()
}

func (v *Volume) AddContainer(containerId string) {
	v.lock.Lock()
	v.containers[containerId] = struct{}{}
	v.lock.Unlock()
}

func (v *Volume) toDisk() error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	pth, err := v.jsonPath()
	if err != nil {
		return err
	}

	_, err = os.Stat(pth)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(pth), 0750); err != nil {
			return err
		}
	}

	return ioutil.WriteFile(pth, data, 0660)
}

func (v *Volume) fromDisk() error {
	v.lock.Lock()
	defer v.lock.Unlock()

	pth, err := v.jsonPath()
	if err != nil {
		return err
	}

	data, err := ioutil.ReadFile(pth)
	if err != nil {
		return err
	}

	var config *volumeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("error getting driver from volume config json: %v", err)
	}
	driver, err := volumedriver.NewDriver(config.DriverName, config.Opts)
	if err != nil {
		return err
	}
	v.Driver = driver
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("error reading volume config json: %v", err)
	}

	return nil
}

func (v *Volume) Create() error {
	if err := v.Driver.Create(); err != nil {
		return err
	}

	return v.toDisk()
}

func (v *Volume) jsonPath() (string, error) {
	return v.getRootResourcePath("config.json")
}
func (v *Volume) getRootResourcePath(path string) (string, error) {
	cleanPath := filepath.Join("/", path)
	return symlink.FollowSymlinkInScope(filepath.Join(v.configPath, cleanPath), v.configPath)
}
