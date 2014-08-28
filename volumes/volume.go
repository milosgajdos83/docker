package volumes

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/utils"
)

type Volume struct {
	ID          string
	Path        string
	IsBindMount bool
	Writable    bool
	Containers  map[string]struct{}
	configPath  string
	repository  *Repository
	sync.Mutex
}

func IdFromPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return utils.SumString(path), nil
}

func (v *Volume) IsDir() (bool, error) {
	stat, err := os.Stat(v.Path)
	if err != nil {
		return false, err
	}

	return stat.IsDir(), nil
}

func (v *Volume) RemoveContainer(containerId string) {
	v.Lock()
	delete(v.Containers, containerId)
	v.Unlock()
}

func (v *Volume) AddContainer(containerId string) {
	v.Lock()
	v.Containers[containerId] = struct{}{}
	v.Unlock()
}

func (v *Volume) createIfNotExist() error {
	if stat, err := os.Stat(v.Path); err != nil && os.IsNotExist(err) {
		if stat.IsDir() {
			os.MkdirAll(v.Path, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(v.Path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(v.Path, os.O_CREATE, 0755)
		if err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

func (v *Volume) initialize() error {
	v.Lock()
	defer v.Unlock()

	if err := v.createIfNotExist(); err != nil {
		return err
	}

	if err := os.MkdirAll(v.configPath, 0755); err != nil {
		return err
	}
	jsonPath, err := v.jsonPath()
	if err != nil {
		return err
	}
	f, err := os.Create(jsonPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return v.toDisk()
}

func (v *Volume) ToDisk() error {
	v.Lock()
	defer v.Unlock()
	return v.toDisk()
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

	return ioutil.WriteFile(pth, data, 0666)
}
func (v *Volume) FromDisk() error {
	pth, err := v.jsonPath()
	if err != nil {
		return err
	}

	data, err := ioutil.ReadFile(pth)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func (v *Volume) jsonPath() (string, error) {
	return v.getRootResourcePath("config.json")
}
func (v *Volume) getRootResourcePath(path string) (string, error) {
	cleanPath := filepath.Join("/", path)
	return symlink.FollowSymlinkInScope(filepath.Join(v.configPath, cleanPath), v.configPath)
}
