package mounter

import (
	"bufio"
	"fmt"
	"os"
	fp "path/filepath"
	"strings"

	"github.com/docker/docker/pkg/fileutils"
	"github.com/docker/docker/pkg/mount"
	"github.com/pkg/errors"

	"github.com/asmyasnikov/droot/log"
	"github.com/asmyasnikov/droot/osutil"
)

// DROOT_ENV_FILE_PATH is the file path of list of environment variables for `droot run`.
const DROOT_BINDS_FILE_PATH = ".drootbinds"

type Mounter struct {
	rootDir string
}

func NewMounter(rootDir string) *Mounter {
	return &Mounter{rootDir: rootDir}
}

func parseBindOption(bindOption string) (hostDir string, containerDir string, rw bool, err error) {
	d := strings.SplitN(bindOption, ":", 3)
	switch len(d) {
	case 3:
		hostDir, containerDir = d[0], d[1]
		rw = strings.ToLower(d[2]) != "rw"
	case 2:
		hostDir, containerDir = d[0], d[1]
		rw = true
	case 1:
		hostDir, containerDir = d[0], d[0]
		rw = true
	default:
		return hostDir, containerDir, rw, fmt.Errorf("Unknown bind option '%s'", bindOption)
	}
	if !fp.IsAbs(hostDir) {
		return hostDir, containerDir, rw, fmt.Errorf("%s is not an absolute path", hostDir)
	}
	if !fp.IsAbs(containerDir) {
		return hostDir, containerDir, rw, fmt.Errorf("%s is not an absolute path", containerDir)
	}
	return fp.Clean(hostDir), fp.Clean(containerDir), rw, nil
}

func ResolveRootDir(dir string) (string, error) {
	var err error

	if !osutil.ExistsDir(dir) {
		return dir, errors.Errorf("No such directory %s:", dir)
	}

	dir, err = fp.Abs(dir)
	if err != nil {
		return dir, err
	}

	if osutil.IsSymlink(dir) {
		dir, err = fp.EvalSymlinks(dir)
		if err != nil {
			return dir, err
		}
	}

	return dir, nil
}

func (m *Mounter) MountSysProc() error {
	// mount -t proc proc {{rootDir}}/proc
	if err := osutil.MountIfNotMounted("proc", fp.Join(m.rootDir, "/proc"), "proc", ""); err != nil {
		return errors.Errorf("Failed to mount /proc: %s", err)
	}
	// mount --rbind /sys {{rootDir}}/sys
	if err := osutil.MountIfNotMounted("/sys", fp.Join(m.rootDir, "/sys"), "none", "rbind"); err != nil {
		return errors.Errorf("Failed to mount /sys: %s", err)
	}
	// mount --make-rslave /sys {{rootDir}}/sys
	if err := osutil.ForceMount("", fp.Join(m.rootDir, "/sys"), "none", "rslave"); err != nil {
		return errors.Errorf("Failed to mount --make-rslave /sys: %s", err)
	}

	return nil
}

func containerBinds(path string) (binds []string, err error) {
	if !osutil.ExistsFile(path) {
		return binds, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		l := strings.Trim(scanner.Text(), " \n\t")
		if len(l) == 0 {
			continue
		}
		binds = append(binds, l)
	}
	return binds, nil
}

func (m *Mounter) BindMounts(bindOpts []string, path string) error {
	binds, err := containerBinds(path)
	if err != nil {
		return err
	}
	for _, bindOption := range append(binds, bindOpts...) {
		hostDir, containerDir, rw, err := parseBindOption(bindOption)
		if err != nil {
			return err
		}
		if rw {
			if err := m.bindMount(hostDir, containerDir); err != nil {
				return errors.Wrapf(err, "Failed to bind read-write mount point %s", bindOpts)
			}
		} else {
			if err := m.roBindMount(hostDir, containerDir); err != nil {
				return errors.Wrapf(err, "Failed to bind read-only mount point %s", bindOpts)
			}
		}
	}
	return nil
}

func (m *Mounter) bindMount(hostDir, containerDir string) error {
	containerDir = fp.Join(m.rootDir, containerDir)

	if ok := osutil.IsDirEmpty(hostDir); ok {
		if _, err := os.Create(fp.Join(hostDir, ".droot.keep")); err != nil {
			return err
		}
	}

	if err := fileutils.CreateIfNotExists(containerDir, true); err != nil { // mkdir -p
		return err
	}

	if err := osutil.MountIfNotMounted(hostDir, containerDir, "none", "bind,rw"); err != nil {
		return err
	}

	return nil
}

func (m *Mounter) roBindMount(hostDir, containerDir string) error {
	if err := m.bindMount(hostDir, containerDir); err != nil {
		return err
	}

	containerDir = fp.Join(m.rootDir, containerDir)

	if err := osutil.ForceMount(hostDir, containerDir, "none", "remount,ro,bind"); err != nil {
		return err
	}

	return nil
}

func (m *Mounter) getMountsRoot() ([]*mount.Info, error) {
	mounts, err := mount.GetMounts()
	if err != nil {
		return nil, err
	}

	targets := make([]*mount.Info, 0)
	for _, mo := range mounts {
		if strings.HasPrefix(mo.Mountpoint, m.rootDir) {
			targets = append(targets, mo)
		}
	}

	return targets, nil
}

func (m *Mounter) UmountRoot() error {
	mounts, err := m.getMountsRoot()
	if err != nil {
		return err
	}

	for _, mo := range mounts {
		if err := mount.Unmount(mo.Mountpoint); err != nil {
			return err
		}
		log.Debug("umount:", mo.Mountpoint)
	}

	return nil
}
