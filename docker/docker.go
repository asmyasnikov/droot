package docker

import (
	"archive/tar"
	"bytes"
	"github.com/asmyasnikov/droot/environ"
	"github.com/asmyasnikov/droot/mounter"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"golang.org/x/net/context" // docker/docker don't use 'context' as standard package.
	"io"
	"strings"
)

// dockerAPI is an interface for stub testing.
type dockerAPI interface {
	ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, containerName string) (container.ContainerCreateCreatedBody, error)
	ContainerStart(ctx context.Context, containerID string, options types.ContainerStartOptions) error
	ContainerWait(ctx context.Context, containerID string) (int64, error)
	ContainerExport(ctx context.Context, containerID string) (io.ReadCloser, error)
	ContainerRemove(ctx context.Context, containerID string, options types.ContainerRemoveOptions) error
}

// Client represents a Docker API client.
type Client struct {
	docker *client.Client
}

// New creates the Client instance.
func New() (*Client, error) {
	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}
	if _, err := cli.Ping(context.Background()); err != nil {
		return nil, err
	}
	return &Client{docker: cli}, nil
}

func (c *Client) Inspect(ctx context.Context, id string) (info *types.ContainerJSON, needStop bool, needRemove bool, err error) {
	// check first existing container
	inspect, err := c.docker.ContainerInspect(ctx, id)
	if err == nil {
		if inspect.State.Running {
			return &inspect, false, false, nil
		}
		if err := c.docker.ContainerStart(ctx, inspect.ID, types.ContainerStartOptions{}); err != nil {
			return nil, false, false, errors.Wrapf(err, "Failed to start container %s", inspect.ID)
		}
		return &inspect, true, false, nil
	}
	container, err := c.docker.ContainerCreate(ctx, &container.Config{
		Image:      id,
		User:       "root",       // Avoid permission denied error
	}, nil, nil, "")
	if err != nil {
		return nil, false, true, errors.Wrapf(err, "Failed to create container from image %s", id)
	}
	info, needStop, _, err = c.Inspect(ctx, container.ID)
	return info, needStop, true, err
}

func (c *Client) Stop(ctx context.Context, containerID string) (error) {
	if err := c.docker.ContainerStop(ctx, containerID, nil); err != nil {
		return errors.Wrapf(err, "Failed to remove container %s", containerID)
	}
	return nil
}

func (c *Client) Remove(ctx context.Context, containerID string) (error) {
	if err := c.docker.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		Force: true,
	}); err != nil {
		return errors.Wrapf(err, "Failed to remove container %s", containerID)
	}
	return nil
}

func (c *Client) writeFakeFile(w *tar.Writer, path string, body []byte, mode int) (error) {
	return c.write(
		w,
		&tar.Header{
			Uname:    "root",
			Gname:    "root",
			Mode:     int64(mode),
			Name:     path,
			Typeflag: tar.TypeReg,
			Size:     int64(len(body)),
		},
		body,
	)
}

func (c *Client) write(w *tar.Writer, h *tar.Header, b []byte) (error) {
	if err := w.WriteHeader(h); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	return nil
}


// ExportImage exports a docker image into the archive of filesystem.
func (c *Client) Export(ctx context.Context, containerID string, info *types.ContainerJSON) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	go func() {
		w := tar.NewWriter(writer)
		if err := c.writeFakeFile(
			w,
			environ.DROOT_ENV_FILE_PATH,
			[]byte(strings.Join(
				info.Config.Env,
				"\n",
			) + "\n\n"),
			0644,
		); err != nil {
			writer.CloseWithError(errors.Wrapf(err, "Failed to write envs"))
			return
		}
		if err := c.writeFakeFile(
			w,
			mounter.DROOT_BINDS_FILE_PATH,
			[]byte(strings.Join(
				func() (binds []string) {
					for _, m := range info.Mounts {
						if m.Type != mount.TypeBind {
							continue
						}
						binds = append(
							binds,
							m.Source + ":" + m.Destination + func() string {
								if m.RW {
									return ""
								}
								return ":ro"
							}(),
						)
					}
					return binds
				}(),
				"\n",
			) + "\n\n"),
			0644,
		); err != nil {
			writer.CloseWithError(errors.Wrapf(err, "Failed to write binds"))
			return
		}
		body, err := c.docker.ContainerExport(ctx, containerID)
		if err != nil {
			writer.CloseWithError(errors.Wrapf(err, "Failed to export container %s", containerID))
			return
		}
		r := tar.NewReader(body)
		buffer := new(bytes.Buffer)
		for {
			h, err := r.Next()
			if err  == io.EOF {
				break
			}
			if err != nil {
				writer.CloseWithError(errors.Wrapf(err, "Failed to read contents of container %s after export", containerID))
				return
			}
			buffer.Reset()
			buffer.ReadFrom(r)
			if err := c.write(w, h, buffer.Bytes()); err != nil {
				writer.CloseWithError(errors.Wrapf(err, "Failed to copy %s from container %s", h.Name, containerID))
				return
			}
		}
		w.Close()
		writer.Close()
	}()
	return reader, nil
}
