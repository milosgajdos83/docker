package daemon

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/docker/archive"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/pkg/log"
	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/volumes"
)

type Mount struct {
	MountToPath string
	container   *Container
	volume      *volumes.Volume
	Writable    bool
}

func (container *Container) prepareVolumes() error {
	if container.Volumes == nil || len(container.Volumes) == 0 {
		container.Volumes = make(map[string]string)
		container.VolumesRW = make(map[string]bool)
		if err := container.applyVolumesFrom(); err != nil {
			return err
		}
	}

	return container.createVolumes()
}

func (container *Container) createVolumes() error {
	mounts, err := container.parseVolumeMountConfig()
	if err != nil {
		return err
	}

	for _, mnt := range mounts {
		mnt.initialize()
	}

	return nil
}

func (m *Mount) initialize() error {
	// This is the full path to container fs + mntToPath
	containerMntPath, err := symlink.FollowSymlinkInScope(filepath.Join(m.container.basefs, m.MountToPath), m.container.basefs)
	if err != nil {
		return err
	}
	m.container.VolumesRW[m.MountToPath] = m.Writable
	m.container.Volumes[m.MountToPath] = m.volume.Path
	m.volume.AddContainer(m.container.ID)
	if m.Writable && !m.volume.IsBindMount {
		// Copy whatever is in the container at the mntToPath to the volume
		copyExistingContents(containerMntPath, m.volume.Path)
	}

	return nil
}

func (container *Container) VolumeIDs() ([]string, error) {
	var volumeIDs []string
	for _, mnt := range container.VolumeMounts() {
		volumeIDs = append(volumeIDs, mnt.volume.ID)
	}

	return volumeIDs, nil
}

func (container *Container) derefVolumes() error {
	ids, err := container.VolumeIDs()
	if err != nil {
		return err
	}

	for _, id := range ids {
		vol := container.daemon.volumes.Get(id)
		if vol == nil {
			log.Debugf("Volume %s was not found and could not be dereferenced", id)
			continue
		}

		vol.RemoveContainer(container.ID)
	}

	return nil
}

func (container *Container) parseVolumeMountConfig() (map[string]*Mount, error) {
	var mounts = make(map[string]*Mount)
	// Get all the bind mounts
	for _, spec := range container.hostConfig.Binds {
		path, mountToPath, writable, err := parseBindMountSpec(spec)
		if err != nil {
			return nil, err
		}
		// Get the volume ID that would be used by this path
		id, err := volumes.IdFromPath(path)
		if err != nil {
			return nil, err
		}
		// Check if a volume already exists for this and use it
		vol := container.daemon.volumes.Get(id)
		if vol == nil {
			vol, err = container.daemon.volumes.NewVolume(path, writable)
			if err != nil {
				return nil, err
			}
		}
		mounts[mountToPath] = &Mount{container: container, volume: vol, MountToPath: mountToPath, Writable: writable}
	}

	// Get the rest of the volumes
	for path := range container.Config.Volumes {
		// Check if this is already added as a bind-mount
		if _, exists := mounts[path]; exists {
			continue
		}

		vol, err := container.daemon.volumes.NewVolume("", true)
		if err != nil {
			return nil, err
		}
		mounts[path] = &Mount{container: container, MountToPath: path, volume: vol, Writable: true}
	}

	return mounts, nil
}

func parseBindMountSpec(spec string) (string, string, bool, error) {
	var (
		path, mountToPath string
		writable          bool
		arr               = strings.Split(spec, ":")
	)

	switch len(arr) {
	case 2:
		path = arr[0]
		mountToPath = arr[1]
		writable = true
	case 3:
		path = arr[0]
		mountToPath = arr[1]
		writable = validMountMode(arr[2]) && arr[2] == "rw"
	default:
		return "", "", false, fmt.Errorf("Invalid volume specification: %s", spec)
	}

	if !filepath.IsAbs(path) {
		return "", "", false, fmt.Errorf("cannot bind mount volume: %s volume paths must be absolute.", path)
	}

	return path, mountToPath, writable, nil
}

func (container *Container) applyVolumesFrom() error {
	volumesFrom := container.hostConfig.VolumesFrom

	for _, spec := range volumesFrom {
		mounts, err := parseVolumesFromSpec(container.daemon, spec)
		if err != nil {
			return err
		}

		for _, mnt := range mounts {
			mnt.container = container
			if err = mnt.initialize(); err != nil {
				return err
			}
		}
	}
	return nil
}

func validMountMode(mode string) bool {
	validModes := map[string]bool{
		"rw": true,
		"ro": true,
	}

	return validModes[mode]
}

func (container *Container) setupMounts() error {
	mounts := []execdriver.Mount{
		{container.ResolvConfPath, "/etc/resolv.conf", true, true},
	}

	if container.HostnamePath != "" {
		mounts = append(mounts, execdriver.Mount{container.HostnamePath, "/etc/hostname", true, true})
	}

	if container.HostsPath != "" {
		mounts = append(mounts, execdriver.Mount{container.HostsPath, "/etc/hosts", true, true})
	}

	// Mount user specified volumes
	// Note, these are not private because you may want propagation of (un)mounts from host
	// volumes. For instance if you use -v /usr:/usr and the host later mounts /usr/share you
	// want this new mount in the container
	for r, v := range container.Volumes {
		mounts = append(mounts, execdriver.Mount{v, r, container.VolumesRW[r], false})
	}

	container.command.Mounts = mounts

	return nil
}

func parseVolumesFromSpec(daemon *Daemon, spec string) (map[string]*Mount, error) {
	specParts := strings.SplitN(spec, ":", 2)
	if len(specParts) == 0 {
		return nil, fmt.Errorf("Malformed volumes-from specification: %s", spec)
	}

	c := daemon.Get(specParts[0])
	if c == nil {
		return nil, fmt.Errorf("Container %s not found. Impossible to mount its volumes", specParts[0])
	}

	mounts := c.VolumeMounts()

	if len(specParts) == 2 {
		mode := specParts[1]
		if validMountMode(mode) {
			return nil, fmt.Errorf("Invalid mode for volumes-from: %s", mode)
		}

		// Set the mode for the inheritted volume
		for _, mnt := range mounts {
			mnt.Writable = (mode != "ro") && mnt.Writable
		}
	}

	return mounts, nil
}

func (container *Container) VolumeMounts() map[string]*Mount {
	mounts := make(map[string]*Mount)

	for mountToPath, path := range container.Volumes {
		if v := container.daemon.volumes.FindByPath(path); v != nil {
			mounts[mountToPath] = &Mount{volume: v, container: container, MountToPath: mountToPath, Writable: container.VolumesRW[mountToPath]}
		}
	}

	return mounts
}

func copyExistingContents(source, destination string) error {
	volList, err := ioutil.ReadDir(source)
	if err != nil {
		return err
	}

	if len(volList) > 0 {
		srcList, err := ioutil.ReadDir(destination)
		if err != nil {
			return err
		}

		if len(srcList) == 0 {
			// If the source volume is empty copy files from the root into the volume
			if err := archive.CopyWithTar(source, destination); err != nil {
				return err
			}
		}
	}

	return copyOwnership(source, destination)
}

// copyOwnership copies the permissions and uid:gid of the source file
// into the destination file
func copyOwnership(source, destination string) error {
	var stat syscall.Stat_t

	if err := syscall.Stat(source, &stat); err != nil {
		return err
	}

	if err := os.Chown(destination, int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}

	return os.Chmod(destination, os.FileMode(stat.Mode))
}
