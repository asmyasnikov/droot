package docker

import (
	"bytes"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"golang.org/x/net/context" // docker/docker don't use 'context' as standard package.
	"io"
	"archive/tar"
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

func (c *Client) Inspect(ctx context.Context, id string) (info *types.ContainerJSON, containerId2Stop *string, err error) {
	// check first existing container
	inspect, err := c.docker.ContainerInspect(ctx, id)
	if err == nil {
		if inspect.State.Running {
			return &inspect, nil, nil
		}
		if err := c.docker.ContainerStart(ctx, inspect.ID, types.ContainerStartOptions{}); err != nil {
			_ = c.Remove(ctx, inspect.ID)
			return nil, nil, errors.Wrapf(err, "Failed to start container %s", inspect.ID)
		}
		return &inspect, &inspect.ID, nil
	}
	container, err := c.docker.ContainerCreate(ctx, &container.Config{
		Image:      id,
		User:       "root",       // Avoid permission denied error
	}, nil, nil, "")
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to create container from image %s", id)
	}
	return c.Inspect(ctx, container.ID)
}

func (c *Client) Remove(ctx context.Context, containerID string) (error) {
	if err := c.docker.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		Force: true,
	}); err != nil {
		return errors.Wrapf(err, "Failed to remove container %s", containerID)
	}
	return nil
}

const DROOT_ENV_FILE_PATH = ".drootenv"
const DROOT_ENTRY_FILE_PATH = ".drootentry.sh"

func (c *Client) writeFakeFile(w *tar.Writer, path string, body string) (error) {
	return c.write(
		w,
		&tar.Header{
			Uname:    "root",
			Gname:    "root",
			Mode:     int64(0777),
			Name:     path,
			Typeflag: tar.TypeReg,
			Size:     int64(len(body)),
		},
		[]byte(body),
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
// Save an environ of the docker image into `/.drootenv` to preserve it.
func (c *Client) Export(ctx context.Context, containerID string, info *types.ContainerJSON) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	go func() {
		w := tar.NewWriter(writer)
		if err := c.writeFakeFile(w, DROOT_ENV_FILE_PATH, strings.Join(info.Config.Env, "\n")); err != nil {
			writer.CloseWithError(errors.Wrapf(err, "Failed to write envs"))
			return
		}
		if err := c.writeFakeFile(w, DROOT_ENTRY_FILE_PATH, strings.Join(append(info.Config.Entrypoint, info.Config.Cmd...), " ")); err != nil {
			writer.CloseWithError(errors.Wrapf(err, "Failed to write entrypoint"))
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
