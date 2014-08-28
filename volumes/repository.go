package volumes

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/utils"
)

type VolumeInUseError struct {
	Err error
}

func (e *VolumeInUseError) Error() string {
	return fmt.Sprintf("%v", e.Err)
}

type Repository struct {
	configPath string
	driver     graphdriver.Driver
	volumes    map[string]*Volume
	sync.Mutex
}

func VolumeInUse(err error) bool {
	switch interface{}(err).(type) {
	case *VolumeInUseError:
		return true
	default:
		return false
	}
}

func NewRepository(configPath string, driver graphdriver.Driver) (*Repository, error) {
	abspath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}

	// Create the config path
	if err := os.MkdirAll(abspath, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	repo := &Repository{
		driver:     driver,
		configPath: abspath,
		volumes:    make(map[string]*Volume),
	}

	return repo, repo.restore()
}

func (r *Repository) NewVolume(path string, writable bool) (*Volume, error) {
	var (
		isBindMount bool
		err         error
		id          string
	)
	if path != "" {

		id, err = IdFromPath(path)
		if err != nil {
			return nil, err
		}
		isBindMount = true
	}

	if path == "" {
		id, path, err = r.createNewVolumePath()
		if err != nil {
			return nil, err
		}
	}

	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}

	v := &Volume{
		ID:          id,
		Path:        path,
		repository:  r,
		Writable:    writable,
		Containers:  make(map[string]struct{}),
		configPath:  r.configPath + "/" + id,
		IsBindMount: isBindMount,
	}

	if err := v.initialize(); err != nil {
		return nil, err
	}
	if err := r.Add(v); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *Repository) restore() error {
	dir, err := ioutil.ReadDir(r.configPath)
	if err != nil {
		return err
	}

	var ids []string
	for _, v := range dir {
		id := v.Name()
		if r.driver.Exists(id) {
			ids = append(ids, id)
		}
	}
	return nil
}

func (r *Repository) FindByPath(path string) *Volume {
	for _, vol := range r.volumes {
		if path == vol.Path {
			return vol
		}
	}
	return nil
}

func (r *Repository) Get(id string) *Volume {
	r.Lock()
	vol := r.volumes[id]
	r.Unlock()
	return vol
}

func (r *Repository) get(id string) *Volume {
	return r.volumes[id]
}

func (r *Repository) Add(volume *Volume) error {
	r.Lock()
	defer r.Unlock()
	if vol := r.get(volume.ID); vol != nil {
		return fmt.Errorf("Volume exists: %s", volume.ID)
	}
	r.volumes[volume.ID] = volume
	return nil
}

func (r *Repository) Remove(volume *Volume) {
	r.Lock()
	r.remove(volume)
	r.Unlock()
}

func (r *Repository) remove(volume *Volume) {
	delete(r.volumes, volume.ID)
}

func (r *Repository) Delete(id string) error {
	r.Lock()
	defer r.Unlock()
	volume := r.get(id)
	if volume == nil {
		return fmt.Errorf("Volume %s does not exist", id)
	}
	if len(volume.Containers) > 0 {
		return &VolumeInUseError{fmt.Errorf("Volume %s is being used and cannot be removed", volume.ID)}
	}

	if volume.IsBindMount {
		return fmt.Errorf("Volume %s is a bind-mount and cannot be removed", volume.ID)
	}

	if err := os.RemoveAll(volume.configPath); err != nil {
		return err
	}

	if err := r.driver.Remove(volume.ID); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	r.remove(volume)
	return nil
}

func (r *Repository) createNewVolumePath() (string, string, error) {
	id := utils.GenerateRandomID()
	if err := r.driver.Create(id, ""); err != nil {
		return "", "", err
	}

	path, err := r.driver.Get(id, "")
	if err != nil {
		return "", "", fmt.Errorf("Driver %s failed to get volume rootfs %s: %s", r.driver, id, err)
	}

	return id, path, nil
}
