package commands

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/mount"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/asmyasnikov/droot/docker"
)

var CommandArgExport = "-o OUTPUT DOCKER_REPOSITORY[:TAG]"
var CommandExport = cli.Command{
	Name:   "export",
	Usage:  "Export a container's filesystem as a tar archive",
	Action: fatalOnError(doExport),
	Flags: []cli.Flag{
		cli.StringFlag{Name: "o, output", Usage: "Write to a file, instead of STDOUT"},
	},
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
	docker, err := docker.New()
	if err != nil {
		return err
	}
	ctx := context.Background()
	info, containerID, err := docker.Inspect(ctx, id)
	defer func() {
		if containerID != nil {
			docker.Remove(ctx, *containerID)
		}
	}()
	if err != nil {
		return err
	}
	reader, err := docker.Export(
		ctx,
		func() string {
			if containerID != nil {
				return *containerID
			}
			return info.ID
		}(),
		info,
	)
	if err != nil {
		return err
	}
	defer reader.Close()
	writer := os.Stdout
	if output := c.String("output"); output != "" {
		file, err := os.Create(output)
		if err != nil {
			return err
		}
		writer = file
	}
	defer func() {
		writer.Close()
	}()
	if _, err := io.Copy(writer, reader); err != nil {
		return err
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
		cmd += func() string {
			if m.RW {
				return " --bind "
			}
			return " --robind "
		}() + m.Source + ":" + m.Destination
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
	cmd += " --root [container directory]"
	cmd += " -- " + func(s string) string {
		if len(s) == 0 {
			return s
		}
		return "[" + s + "] -c "
	}(strings.Join(info.Config.Shell, "|")) + "\"" + strings.Join(append(info.Config.Entrypoint, info.Config.Cmd...), " ") + "\"\n"
	fmt.Fprintln(os.Stderr, "Run droot with command (save this for future use):\n")
	fmt.Fprintln(os.Stderr, cmd)
	if len(attentions) > 0 {
		fmt.Fprintln(os.Stderr, "Attentions:\n", attentions)
	}
	return nil
}
