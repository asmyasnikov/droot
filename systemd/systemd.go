package systemd

import (
	"bytes"
	"fmt"
	"github.com/asmyasnikov/droot/osutil"
	"github.com/docker/docker/api/types"
	"github.com/pkg/errors"
	"os"
	"strings"
	"text/template"
)

const systemdScript = `[Unit]
Description={{.Description}}
{{- range $i, $dep := .Dependencies}} 
{{$dep}} {{end}}

[Service]
StartLimitInterval=5
StartLimitBurst=10
{{if .CPUQuota}}CPUQuota={{.CPUQuota}}%{{end}}
{{if .MemoryLimit}}MemoryLimit={{.MemoryLimit}}M{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory}}/{{end}}
ExecStart={{.ExecStart}}
ExecStopPost={{.ExecStopPost}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .Restart}}Restart={{.Restart}}{{end}}

[Install]
WantedBy=multi-user.target
`

func config(root string, info *types.ContainerJSON) ([]byte, error) {
	ex, err := os.Executable()
	if err != nil {
		return nil, err
	}
	buffer := &bytes.Buffer{}
	err = template.Must(template.New("").Funcs(map[string]interface{}{
		"cmd": func(s string) string {
			return `"` + strings.Replace(s, `"`, `\"`, -1) + `"`
		},
		"cmdEscape": func(s string) string {
			return strings.Replace(s, " ", `\x20`, -1)
		},
	}).Parse(systemdScript)).Execute(buffer, &struct {
		Description      string
		Dependencies     []string
		ExecStart        string
		ExecStopPost     string
		WorkingDirectory *string
		UserName         *string
		ReloadSignal     int
		Restart          string
		CPUQuota         *int
		MemoryLimit      *int
	}{
		info.Config.Image,
		[]string{
			"After=network.target",
		},
		ex + " run --cp --user " + func() string {
			if len(info.Config.User) > 0 {
				return info.Config.User
			}
			return "root"
		}() + " --root " + root + " -- \"" + strings.Join(append(info.Config.Entrypoint, info.Config.Cmd...), "\" \"") + "\"",
		ex + " umount --root " + root,
		func() *string {
			if len(info.Config.WorkingDir) > 0 {
				return &info.Config.WorkingDir
			}
			return nil
		}(),
		func() *string {
			if len(info.Config.User) > 0 {
				return &info.Config.User
			}
			return nil
		}(),
		9,
		func() string {
			if info.HostConfig.RestartPolicy.IsAlways() {
				return "always"
			}
			if info.HostConfig.RestartPolicy.IsOnFailure() {
				return "on-failure"
			}
			return "no"
		}(),
		func() *int {
			if info.HostConfig.NanoCPUs > 0 {
				percentage := int(float32(info.HostConfig.NanoCPUs) / 10000000.0)
				return &percentage
			}
			return nil
		}(),
		func() *int {
			if info.HostConfig.Memory > 0 {
				memory := int(info.HostConfig.Memory / 1024 / 1024)
				return &memory
			}
			return nil
		}(),
	})
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func Install(path, name string, info *types.ContainerJSON) error {
	configPath := "/lib/systemd/system/" + name + ".service"
	if osutil.ExistsFile(configPath) {
		return fmt.Errorf("Systemd service config %s already exists", config)
	}
	b, err := config(path, info);
	if err != nil {
		return errors.Wrapf(err, "Failed to compile systemd config")
	}
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return errors.Wrapf(err, "Failed to create systemd config file")
	}
	if _, err := file.Write(b); err != nil {
		return errors.Wrapf(err, "Failed to write systemd config file")
	}
	return nil
}