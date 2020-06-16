package commands

import (
	"fmt"
	"os"
	fp "path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/asmyasnikov/droot/environ"
	"github.com/asmyasnikov/droot/log"
	"github.com/asmyasnikov/droot/mounter"
	"github.com/asmyasnikov/droot/osutil"
)

var CommandArgRun = "--root ROOT_DIR [--user USER] [--group GROUP] [--bind SRC-PATH[:DEST-PATH]] [--robind SRC-PATH[:DEST-PATH]] [--no-dropcaps] -- COMMAND"
var CommandRun = cli.Command{
	Name:   "run",
	Usage:  "Run command in container",
	Action: fatalOnError(doRun),
	Flags: []cli.Flag{
		cli.StringFlag{Name: "root, r", Usage: "Root directory path for chrooting"},
		cli.StringFlag{Name: "user, u", Usage: "User (ID or name) to switch before running the program"},
		cli.StringFlag{Name: "group, g", Usage: "Group (ID or name) to switch to"},
		cli.StringSliceFlag{
			Name:  "bind, b",
			Value: &cli.StringSlice{},
			Usage: "Bind mount directory (can be specifies multiple times)",
		},
		cli.StringSliceFlag{
			Name:  "robind",
			Value: &cli.StringSlice{},
			Usage: "Readonly bind mount directory (can be specifies multiple times)",
		},
		cli.BoolFlag{
			Name:  "copy-files, cp",
			Usage: "Copy host files to container such as /etc/group, /etc/passwd, /etc/resolv.conf, /etc/hosts",
		},
		cli.BoolFlag{Name: "no-dropcaps", Usage: "Provide COMMAND's process in chroot with root permission (dangerous)"},
		cli.StringSliceFlag{
			Name:  "env, e",
			Value: &cli.StringSlice{},
			Usage: "Set environment variables",
		},
	},
}

var copyFiles = []string{
	"etc/group",
	"etc/passwd",
	"etc/resolv.conf",
	"etc/hosts",
}

var keepCaps = map[uint]bool{
	0:  true, // CAP_CHOWN
	1:  true, // CAP_DAC_OVERRIDE
	2:  true, // CAP_DAC_READ_SEARCH
	3:  true, // CAP_FOWNER
	6:  true, // CAP_SETGID
	7:  true, // CAP_SETUID
	10: true, // CAP_NET_BIND_SERVICE
}

func doRun(c *cli.Context) error {
	command := c.Args()
	if len(command) < 1 {
		cli.ShowCommandHelp(c, "run")
		return errors.New("command required")
	}

	optRootDir := c.String("root")
	if optRootDir == "" {
		cli.ShowCommandHelp(c, "run")
		return errors.New("--root option required")
	}

	rootDir, err := mounter.ResolveRootDir(optRootDir)
	if err != nil {
		return err
	}

	// Check env format KEY=VALUE
	env := c.StringSlice("env")
	if len(env) > 0 {
		for _, e := range env {
			if len(strings.SplitN(e, "=", 2)) != 2 {
				return errors.Errorf("Invalid env format: %s", e)
			}
		}
	}

	uid, gid := os.Getuid(), os.Getgid()

	if group := c.String("group"); group != "" {
		if gid, err = osutil.LookupGroup(group); err != nil {
			return err
		}
	}
	if user := c.String("user"); user != "" {
		if uid, err = osutil.LookupUser(user); err != nil {
			return err
		}
	}

	// copy files
	if c.Bool("copy-files") {
		for _, f := range copyFiles {
			srcFile, destFile := fp.Join("/", f), fp.Join(rootDir, f)
			if err := osutil.Cp(srcFile, destFile); err != nil {
				return errors.Wrapf(err, "Failed to copy %s", f)
			}
			if err := os.Lchown(destFile, uid, gid); err != nil {
				return errors.Wrapf(err, "Failed to lchown %s", f)
			}
		}
	}

	mnt := mounter.NewMounter(rootDir)

	if err := mnt.MountSysProc(); err != nil {
		return err
	}

	for _, val := range c.StringSlice("bind") {
		hostDir, containerDir, err := parseBindOption(val)
		if err != nil {
			return err
		}
		if err := mnt.BindMount(hostDir, containerDir); err != nil {
			return err
		}
	}
	for _, val := range c.StringSlice("robind") {
		hostDir, containerDir, err := parseBindOption(val)
		if err != nil {
			return err
		}
		if err := mnt.RoBindMount(hostDir, containerDir); err != nil {
			return errors.Wrapf(err, "Failed to robind mount %s", val)
		}
	}

	// create symlinks
	if err := osutil.Symlink("../run/lock", fp.Join(rootDir, "/var/lock")); err != nil {
		return err
	}

	if err := createDevices(rootDir, uid, gid); err != nil {
		return err
	}

	if err := osutil.Chroot(rootDir); err != nil {
		return fmt.Errorf("Failed to chroot: %s", err)
	}

	if !c.Bool("no-dropcaps") {
		if err := osutil.DropCapabilities(keepCaps); err != nil {
			return fmt.Errorf("Failed to drop capabilities: %s", err)
		}
	}

	if err := osutil.Setgid(gid); err != nil {
		return fmt.Errorf("Failed to set group %d: %s", gid, err)
	}
	if err := osutil.Setuid(uid); err != nil {
		return fmt.Errorf("Failed to set user %d: %s", uid, err)
	}

	if osutil.ExistsFile(environ.DROOT_ENV_FILE_PATH) {
		envFromFile, err := environ.GetEnvironFromEnvFile(environ.DROOT_ENV_FILE_PATH)
		if err != nil {
			return fmt.Errorf("Failed to read environ from '%s'", environ.DROOT_ENV_FILE_PATH)
		}
		env, err = environ.MergeEnviron(envFromFile, env)
		if err != nil {
			return fmt.Errorf("Failed to merge environ: %s", err)
		}
	}
	return osutil.Execv(command[0], command[0:], env)
}

func parseBindOption(bindOption string) (string, string, error) {
	var hostDir, containerDir string

	d := strings.SplitN(bindOption, ":", 2)
	if len(d) < 2 {
		hostDir = d[0]
	} else {
		hostDir, containerDir = d[0], d[1]
	}
	if containerDir == "" {
		containerDir = hostDir
	}

	if !fp.IsAbs(hostDir) {
		return hostDir, containerDir, fmt.Errorf("%s is not an absolute path", hostDir)
	}
	if !fp.IsAbs(containerDir) {
		return hostDir, containerDir, fmt.Errorf("%s is not an absolute path", containerDir)
	}

	return fp.Clean(hostDir), fp.Clean(containerDir), nil
}

func createDevices(rootDir string, uid, gid int) error {
	nullDir := fp.Join(rootDir, os.DevNull)
	if err := osutil.Mknod(nullDir, unix.S_IFCHR|uint32(os.FileMode(0666)), 1*256+3); err != nil {
		return err
	}

	if err := os.Lchown(nullDir, uid, gid); err != nil {
		log.Debugf("Failed to lchown %s: %s\n", nullDir, err)
		return err
	}

	zeroDir := fp.Join(rootDir, "/dev/zero")
	if err := osutil.Mknod(zeroDir, unix.S_IFCHR|uint32(os.FileMode(0666)), 1*256+3); err != nil {
		return err
	}

	if err := os.Lchown(zeroDir, uid, gid); err != nil {
		log.Debugf("Failed to lchown %s: %s\n", zeroDir, err)
		return err
	}

	for _, f := range []string{"/dev/random", "/dev/urandom"} {
		randomDir := fp.Join(rootDir, f)
		if err := osutil.Mknod(randomDir, unix.S_IFCHR|uint32(os.FileMode(0666)), 1*256+9); err != nil {
			return err
		}

		if err := os.Lchown(randomDir, uid, gid); err != nil {
			log.Debugf("Failed to lchown %s: %s\n", randomDir, err)
			return err
		}
	}

	return nil
}
