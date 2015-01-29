package volumes

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/utils"

	"github.com/docker/docker/volumes/volumedriver"
	_ "github.com/docker/docker/volumes/volumedriver/host"
	_ "github.com/docker/docker/volumes/volumedriver/vfs"
)

var DriverNotExist = errors.New("driver does not exist")
var IdGenerateErr = errors.New("failed to generate unique volume ID")
var DataExist = errors.New(volumedriver.DataExist.Error() + " - not managing")

type Repository struct {
	configPath string
	storePath  string
	volumes    map[string]*Volume
	idIndex    map[string]*Volume
	driver     graphdriver.Driver
	lock       sync.Mutex
}

func NewRepository(configPath string, storePath string) (*Repository, error) {
	abspath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}

	// Create the config path
	if err := os.MkdirAll(abspath, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	repo := &Repository{
		storePath:  storePath,
		configPath: abspath,
		volumes:    make(map[string]*Volume),
		idIndex:    make(map[string]*Volume),
	}

	return repo, repo.restore()
}

func (r *Repository) restore() error {
	dir, err := ioutil.ReadDir(r.configPath)
	if err != nil {
		return err
	}

	for _, v := range dir {
		id := v.Name()
		vol := &Volume{
			ID:         id,
			configPath: r.configPath + "/" + id,
			containers: make(map[string]struct{}),
		}
		if err := vol.fromDisk(); err != nil {
			if !os.IsNotExist(err) {
				log.Debugf("Error restoring volume: %v", err)
				continue
			}
		}
		if err := r.add(vol); err != nil {
			log.Debugf("Error restoring volume: %v", err)
		}
	}
	return nil
}

func (r *Repository) Get(path string) *Volume {
	r.lock.Lock()
	vol := r.get(path)
	r.lock.Unlock()
	return vol
}

func (r *Repository) get(path string) *Volume {
	return r.volumes[path]
}

func (r *Repository) add(volume *Volume) error {
	if vol := r.get(volume.String()); vol != nil {
		return fmt.Errorf("Volume exists: %s", volume.ID)
	}
	r.volumes[volume.String()] = volume
	r.idIndex[volume.ID] = volume
	return nil
}

func (r *Repository) Delete(id string) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	volume := r.get(id)
	if volume == nil {
		return fmt.Errorf("Volume %s does not exist", id)
	}

	containers := volume.Containers()
	if len(containers) > 0 {
		return fmt.Errorf("Volume %s is being used and cannot be removed: used by containers %s", volume.Path, containers)
	}

	if err := os.RemoveAll(volume.configPath); err != nil {
		return err
	}

	if err := volume.Remove(); err != nil {
		return err
	}

	delete(r.volumes, volume.String())
	delete(r.idIndex, volume.ID)
	return nil
}

func (r *Repository) FindOrCreateVolume(driver string, opts []string) (*Volume, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	if driver == "" {
		driver = "vfs"
	}

	v, err := r.newVolume(driver, opts)
	if err != nil {
		return nil, err
	}

	if v := r.get(v.String()); v != nil {
		log.Debugf("found existing volume for: %s %v", driver, opts)
		return v, nil
	}

	if err := v.Create(); err != nil {
		v.Remove()
		return nil, err
	}

	return v, r.add(v)
}

func (r *Repository) newVolume(driver string, opts []string) (*Volume, error) {
	id, err := r.generateId()
	if err != nil {
		return nil, err
	}
	opts = append(opts, "id="+id)
	opts = append(opts, "home="+filepath.Join(filepath.Dir(r.configPath), driver))

	d, err := volumedriver.NewDriver(driver, opts)
	if err != nil {
		if err == volumedriver.DataExist {
			return nil, DataExist
		}
		return nil, err
	}

	configPath := filepath.Join(r.configPath, id)
	return &Volume{
		ID:           id,
		Driver:       d,
		containers:   make(map[string]struct{}),
		volumeConfig: &volumeConfig{DriverName: driver, Opts: opts},
		configPath:   configPath}, nil
}

func (r *Repository) generateId() (string, error) {
	for i := 0; i < 5; i++ {
		id := utils.GenerateRandomID()
		if _, exists := r.idIndex[id]; !exists {
			return id, nil
		}
	}
	return "", IdGenerateErr
}
