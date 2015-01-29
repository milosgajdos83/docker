package daemon

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/pkg/chrootarchive"
	"github.com/docker/docker/pkg/symlink"
	"github.com/docker/docker/volumes"
	"github.com/docker/libcontainer/label"
)

type volumeMount struct {
	toPath   string
	fromPath string
	mode     string
	copyData bool
	from     string
	driver   string
}

var ErrFileExists = errors.New("can't mount to container path, file exists")

func (m *volumeMount) canMount(container *Container) bool {
	containerPath := filepath.Join(container.basefs, m.toPath)
	toStat, err := os.Stat(containerPath)
	if err != nil {
		// Doesn't exist so we can mount here
		return true
	}

	if m.fromPath == "" {
		// since the fromPath doens't exist just need to check if the container path is a dir
		return toStat.IsDir()
	}

	fromStat, err := os.Stat(filepath.Clean(m.fromPath))
	if err == nil {
		// This would be a bind mount, so need to check if these are the same (file or dir)
		return fromStat.IsDir() == toStat.IsDir()
	}

	return false
}

func (container *Container) prepareVolumes() error {
	if container.Volumes == nil || len(container.Volumes) == 0 {
		container.Volumes = make(map[string]string)
		container.VolumesRW = make(map[string]bool)
	}

	if len(container.hostConfig.VolumesFrom) > 0 && container.AppliedVolumesFrom == nil {
		container.AppliedVolumesFrom = make(map[string]struct{})
	}
	return container.createVolumes()
}

func (container *Container) createVolumes() error {
	mounts := make(map[string]*volumeMount)

	// get the normal volumes
	for path := range container.Config.Volumes {
		path = filepath.Clean(path)
		// skip if there is already a volume for this container path
		if _, exists := container.Volumes[path]; exists {
			continue
		}
		mnt := &volumeMount{
			toPath:   path,
			mode:     "rw",
			copyData: true,
		}
		mounts[mnt.toPath] = mnt
	}

	// Get all the bind mounts
	// track bind paths separately due to #10618
	bindPaths := make(map[string]struct{})
	for _, spec := range container.hostConfig.Binds {
		mnt := &volumeMount{}
		var err error
		mnt.fromPath, mnt.toPath, mnt.mode, err = parseBindMountSpec(spec)
		if err != nil {
			return err
		}

		// #10618
		if _, exists := bindPaths[mnt.toPath]; exists {
			return fmt.Errorf("Duplicate volume mount %s", mnt.toPath)
		}

		bindPaths[mnt.toPath] = struct{}{}
		mounts[mnt.toPath] = mnt
	}

	// Get volumes from
	for _, from := range container.hostConfig.VolumesFrom {
		cID, mode, err := parseVolumesFromSpec(from)
		if err != nil {
			return err
		}
		if _, exists := container.AppliedVolumesFrom[cID]; exists {
			// skip since it's already been applied
			continue
		}

		c, err := container.daemon.Get(cID)
		if err != nil {
			return fmt.Errorf("container %s not found, impossible to mount its volumes", cID)
		}

		for _, mnt := range c.volumeMounts() {
			mode = "rw"
			if mnt.mode == "ro" {
				mode = "ro"
			}
			mnt.mode = mode
			mnt.from = cID
			mounts[mnt.toPath] = mnt
			log.Debugf("setting volume-from %v", mnt)
		}
	}
	for _, mnt := range mounts {
		if !mnt.canMount(container) {
			return ErrFileExists
		}

		// Check if this already exists and skip
		// Don't skip if it is a bind-mount or a volumes-from; sufficient to check if the registered host path matches the new one we parsed from above
		if hostPath, exists := container.Volumes[mnt.toPath]; exists {
			v := container.daemon.volumes.Get(hostPath)
			if v != nil {
				if v.String() == mnt.fromPath {
					continue
				}
				// If the mnt.fromPath does not match the registered volume in container.Volumes, the volume mount is going to be overwritten
				// For this, we need to deref the volume record and try to delete it.  No need to check for errors here since this is a simple cleanup, maybe the volume was is being used by another container at this point.
				v.RemoveContainer(container.ID)
				container.daemon.volumes.Delete(v.Path)
			}
		}
		log.Debugf("creating volume for container: %s, at: %s", container.ID, mnt.toPath)

		// This is the full path to container fs + toPath
		containerMntPath, err := symlink.FollowSymlinkInScope(filepath.Join(container.basefs, mnt.toPath), container.basefs)
		if err != nil {
			return err
		}

		// Create the actual volume
		// If the data exists for this volume already, we should get a volumes.DataExist error
		// In this case, the volumes subsystem doesn't care about it since it's not going to manage it, but it will be mounted into the container
		v, err := container.daemon.volumes.FindOrCreateVolume(mnt.driver, []string{"path=" + mnt.fromPath})
		if err != nil && err != volumes.DataExist {
			return err
		}

		mnt.fromPath = v.String()

		container.VolumesRW[mnt.toPath] = mnt.mode == "rw"
		container.Volumes[mnt.toPath] = mnt.fromPath
		v.AddContainer(container.ID)
		if mnt.from != "" {
			container.AppliedVolumesFrom[mnt.from] = struct{}{}
		}

		log.Debug(mnt)
		if mnt.mode == "rw" && mnt.copyData {
			// Copy whatever is in the container at the toPath to the volume
			log.Debugf("copying contents of %s to %s", mnt.fromPath, mnt.toPath)
			copyExistingContents(containerMntPath, mnt.fromPath)
		}
	}

	return nil
}

// sortedVolumeMounts returns the list of container volume mount points sorted in lexicographic order
func (container *Container) sortedVolumeMounts() []string {
	var mountPaths []string
	for path := range container.Volumes {
		mountPaths = append(mountPaths, path)
	}

	sort.Strings(mountPaths)
	return mountPaths
}

func (container *Container) VolumePaths() map[string]struct{} {
	var paths = make(map[string]struct{})
	for _, path := range container.Volumes {
		paths[path] = struct{}{}
	}
	return paths
}

func (container *Container) registerVolumes() {
	for path := range container.VolumePaths() {
		if v := container.daemon.volumes.Get(path); v != nil {
			v.AddContainer(container.ID)
			continue
		}

		// if container was created with an old daemon, this volume may not be registered so we need to make sure it gets registered
		v, err := container.daemon.volumes.FindOrCreateVolume(path, []string{})
		if err != nil {
			log.Debugf("error registering volume %s: %v", path, err)
			continue
		}
		v.AddContainer(container.ID)
	}
}

func (container *Container) derefVolumes() {
	for path := range container.VolumePaths() {
		vol := container.daemon.volumes.Get(path)
		if vol == nil {
			log.Debugf("Volume %s was not found and could not be dereferenced", path)
			continue
		}
		vol.RemoveContainer(container.ID)
	}
}

func parseBindMountSpec(spec string) (path string, toPath string, mode string, err error) {
	arr := strings.Split(spec, ":")

	switch len(arr) {
	case 2:
		path = arr[0]
		toPath = arr[1]
		mode = "rw"
	case 3:
		path = arr[0]
		toPath = arr[1]
		mode = arr[2]
		if !validMountMode(arr[2]) {
			mode = "rw"
		}
	default:
		return "", "", "", fmt.Errorf("Invalid volume specification: %s", spec)
	}

	if !filepath.IsAbs(path) {
		return "", "", "", fmt.Errorf("cannot bind mount volume: %s volume paths must be absolute.", path)
	}

	path = filepath.Clean(path)
	toPath = filepath.Clean(toPath)
	return path, toPath, mode, nil
}

func parseVolumesFromSpec(spec string) (string, string, error) {
	specParts := strings.SplitN(spec, ":", 2)
	if len(specParts) == 0 {
		return "", "", fmt.Errorf("malformed volumes-from specification: %s", spec)
	}

	var (
		id   = specParts[0]
		mode = "rw"
	)
	if len(specParts) == 2 {
		mode = specParts[1]
		if !validMountMode(mode) {
			return "", "", fmt.Errorf("invalid mode for volumes-from: %s", mode)
		}
	}
	return id, mode, nil
}

func validMountMode(mode string) bool {
	validModes := map[string]bool{
		"rw": true,
		"ro": true,
	}

	return validModes[mode]
}

func (container *Container) setupMounts() error {
	mounts := []execdriver.Mount{}

	// Mount user specified volumes
	// Note, these are not private because you may want propagation of (un)mounts from host
	// volumes. For instance if you use -v /usr:/usr and the host later mounts /usr/share you
	// want this new mount in the container
	// These mounts must be ordered based on the length of the path that it is being mounted to (lexicographic)
	for _, path := range container.sortedVolumeMounts() {
		mounts = append(mounts, execdriver.Mount{
			Source:      container.Volumes[path],
			Destination: path,
			Writable:    container.VolumesRW[path],
		})
	}

	if container.ResolvConfPath != "" {
		mounts = append(mounts, execdriver.Mount{Source: container.ResolvConfPath, Destination: "/etc/resolv.conf", Writable: true, Private: true})
	}

	if container.HostnamePath != "" {
		mounts = append(mounts, execdriver.Mount{Source: container.HostnamePath, Destination: "/etc/hostname", Writable: true, Private: true})
	}

	if container.HostsPath != "" {
		mounts = append(mounts, execdriver.Mount{Source: container.HostsPath, Destination: "/etc/hosts", Writable: true, Private: true})
	}

	for _, m := range mounts {
		if err := label.SetFileLabel(m.Source, container.MountLabel); err != nil {
			return err
		}
	}

	container.command.Mounts = mounts
	return nil
}

func (container *Container) volumeMounts() map[string]*volumeMount {
	mounts := make(map[string]*volumeMount)

	for toPath, path := range container.Volumes {
		v := container.daemon.volumes.Get(path)
		if v == nil {
			// This should never happen
			log.Debugf("reference by container %s to non-existent volume path %s", container.ID, path)
			continue
		}
		mode := "rw"
		if rw, exists := container.VolumesRW[toPath]; exists && !rw {
			mode = "ro"
		}
		mounts[toPath] = &volumeMount{fromPath: path, toPath: toPath, mode: mode}
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
			if err := chrootarchive.CopyWithTar(source, destination); err != nil {
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

func (container *Container) mountVolumes() error {
	for dest, source := range container.Volumes {
		v := container.daemon.volumes.Get(source)
		if v == nil {
			return fmt.Errorf("could not find volume for %s:%s, impossible to mount", source, dest)
		}

		destPath, err := container.getResourcePath(dest)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			destPath = filepath.Join(container.basefs, dest)
		}
		if err := v.Mount(destPath, "rw"); err != nil {
			return err
		}
	}
	return nil
}

func (container *Container) unmountVolumes() error {
	for dest, source := range container.Volumes {
		v := container.daemon.volumes.Get(source)
		if v == nil {
			log.Debugf("could not find volume for %s:%s, impossible to mount", source, dest)
			continue
		}
		destPath, err := container.getResourcePath(dest)
		if err != nil {
			return err
		}

		if err := v.Unmount(destPath); err != nil {
			return err
		}
	}
	return nil
}
