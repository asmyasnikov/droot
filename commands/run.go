package commands

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path"
	fp "path/filepath"

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
	if len(c.StringSlice("robind")) > 0 {
		cli.ShowCommandHelp(c, "run")
		return errors.New("--robind depricated. use --bind HOST_DIR:CONTAINER_DIR[:ro]")
	}

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

	env, err := environ.Environ(c.StringSlice("env"), path.Join(rootDir, environ.DROOT_ENV_FILE_PATH))
	if err != nil {
		return err
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

	if err := mnt.BindMounts(c.StringSlice("bind"), path.Join(rootDir, mounter.DROOT_BINDS_FILE_PATH)); err != nil {
		return err
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

	return osutil.Execv(command[0], command[0:], env)
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
