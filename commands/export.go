package commands

import (
	"archive/tar"
	"context"
	"fmt"
	"github.com/asmyasnikov/droot/systemd"
	"github.com/docker/docker/api/types/mount"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/asmyasnikov/droot/docker"
)

var CommandArgExport = "[-o {OUTPUT_DIRECTORY,OUTPUT_TAR_FILE}] [-i SYSTEMD_SERVICE_NAME] {IMAGE[:TAG],CONTAINER}"
var CommandExport = cli.Command{
	Name:   "export",
	Usage:  "Export a container's filesystem as a tar archive or directory",
	Action: fatalOnError(doExport),
	Flags: []cli.Flag{
		cli.StringFlag{Name: "o, output", Usage: "Write to a file, instead of STDOUT"},
		cli.StringFlag{Name: "i, install", Usage: "Install container as systemd service (if output is a directory)"},
	},
}

type OutType string

const (
	UNKNOWN OutType = "unknown"
	TAR OutType = "tar"
	PIPE OutType = "pipe"
	DIR OutType = "dir"
)

func outType(output string) (OutType, error) {
	if len(output) == 0 {
		return PIPE, nil
	}
	info, err := os.Lstat(output);
	if err != nil || info.Mode().IsRegular() {
		if os.IsNotExist(err) {
			err = nil
		}
		if strings.HasSuffix(output, ".tar") {
			return TAR, err
		} else {
			return DIR, err
		}
	}
	childs, err := ioutil.ReadDir(output);
	if err != nil {
		return UNKNOWN, fmt.Errorf("Cannot read output directory %s", output)
	}
	if len(childs) > 0 {
		return DIR, fmt.Errorf("Output directory %s is not empty", output)
	}
	return DIR, nil
}

func read(reader io.Reader, output string) error {
	oType, err := outType(output)
	if err != nil {
		return err
	}
	switch oType {
	case PIPE:
		if _, err := io.Copy(os.Stdout, reader); err != nil {
			return err
		}
		return nil
	case TAR:
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := io.Copy(file, reader); err != nil {
			return err
		}
		return nil
	case DIR:
		if err := os.MkdirAll(output, 0755); err != nil {
			return err
		}
		tr := tar.NewReader(reader)
		for {
			header, err := tr.Next()
			switch {
			case err == io.EOF:
				return nil
			case err != nil:
				return err
			case header == nil:
				continue
			}
			target := filepath.Join(output, header.Name)
			switch header.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
					return err
				}
			case tar.TypeReg:
				f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
				if err != nil {
					return err
				}
				if _, err := io.Copy(f, tr); err != nil {
					return err
				}
				f.Close()
			case tar.TypeLink:
				err := os.Link(header.Linkname, target)
				if err != nil {
					return err
				}
			case tar.TypeSymlink:
				err := os.Symlink(header.Linkname, target)
				if err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("Not supported output format %s", oType)
	}
}

func doExport(c *cli.Context) error {
	if len(c.Args()) < 1 {
		cli.ShowCommandHelp(c, "export")
		return errors.New("docker imageID/containerID required")
	}
	id := c.Args().Get(0)
	if id == "" {
		cli.ShowCommandHelp(c, "export")
		return errors.New("docker imageID/containerID required")
	}
	output := c.String("output")
	oType, err := outType(output)
	if err != nil {
		return err
	}
	docker, err := docker.New()
	if err != nil {
		return err
	}
	ctx := context.Background()
	info, needStop, needRemove, err := docker.Inspect(ctx, id)
	defer func() {
		if info == nil {
			return
		}
		if needRemove {
			docker.Remove(ctx, info.ID)
		} else if needStop {
			docker.Stop(ctx, info.ID)
		}
	}()
	if err != nil {
		return err
	}
	reader, err := docker.Export(
		ctx,
		info.ID,
		info,
	)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := read(reader, output); err != nil {
		return err
	}
	if oType == DIR && c.IsSet("install") {
		absPath, err := filepath.Abs(output)
		if err != nil {
			return err
		}
		systemd.Install(absPath, c.String("install"), info)
	}
	cmd := "\tdroot run [--cp]"
	if len(info.Config.User) > 0 {
		cmd += " --user " + info.Config.User
	}
	for _, e := range info.Config.Env {
		cmd += " --env " + e
	}
	attentions := ""
	for _, m := range info.Mounts {
		if m.Type != mount.TypeBind {
			attentions += "\tmount point " + m.Source + ":" + m.Destination + " is a " + string(m.Type) + "\n"
			continue
		}
		cmd += m.Source + ":" + m.Destination + func() string {
			if !m.RW {
				return ":ro"
			}
			return ""
		}()
	}
	for p, b := range info.HostConfig.PortBindings {
		attentions += "\tcontainer port binding " + p.Port() + " -> [ " + func() string {
			return strings.Join(func() (bindings []string) {
				for _, s := range b {
					bindings = append(bindings, s.HostIP + ":" + s.HostPort)
				}
				return bindings
			}(), ",")
		}() + " ]\n"
	}
	for _, n := range info.NetworkSettings.Networks {
		if len(n.IPAddress) == 0 {
			continue
		}
		attentions += "\tcontainer have address " + n.IPAddress + " with network gateway " + n.Gateway + "\n"
	}
	if len(info.Config.WorkingDir) > 0 {
		attentions += "\tcontainer have working directory " + info.Config.WorkingDir + "\n"
	}
	for _, l := range info.HostConfig.Ulimits {
		attentions += "\tcontainer have ulimit " + l.String() + "\n"
	}
	if info.ContainerJSONBase.HostConfig.Resources.NanoCPUs > 0 {
		attentions += "\tcontainer have limit cpus 0." + strconv.Itoa(int(info.ContainerJSONBase.HostConfig.Resources.NanoCPUs / 1000000)) + "\n"
	}
	if info.ContainerJSONBase.HostConfig.Resources.Memory > 0 {
		attentions += "\tcontainer have limit memory " + strconv.Itoa(int(info.ContainerJSONBase.HostConfig.Resources.Memory / 1024 / 1024)) + "MB\n"
	}
	cmd += " --root " + func() string {
		if oType == DIR {
			absPath, err := filepath.Abs(output)
			if err != nil {
				return "[container directory]"
			}
			return absPath
		}
		return "[container directory]"
	}()
	cmd += " -- " + strings.Join(append(info.Config.Entrypoint, info.Config.Cmd...), " ") + "\n"
	fmt.Fprintln(os.Stderr, "Run droot with command (save this for future use):")
	fmt.Fprintln(os.Stderr, cmd)
	if len(attentions) > 0 {
		fmt.Fprintln(os.Stderr, "Attentions:\n", attentions)
	}
	return nil
}
