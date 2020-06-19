package environ

import (
	"bufio"
	"github.com/asmyasnikov/droot/osutil"
	"os"
	"strings"

	"github.com/pkg/errors"
)

// DROOT_ENV_FILE_PATH is the file path of list of environment variables for `droot run`.
const DROOT_ENV_FILE_PATH = ".drootenv"

func parseEnv(s string) (string, string, error) {
	kv := strings.SplitN(s, "=", 2)
	if len(kv) != 2 {
		return "", "", errors.Errorf("Invalid env format: %s", s)
	}
	return kv[0], kv[1], nil
}

func containerEnvs(path string) (map[string]string, error) {
	if !osutil.ExistsFile(path) {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	env := make(map[string]string)
	for scanner.Scan() {
		l := strings.Trim(scanner.Text(), " \n\t")
		if len(l) == 0 {
			continue
		}
		if len(strings.Split(l, "=")) != 2 { // line should be `key=value`
			continue
		}
		k, v, err := parseEnv(l)
		if err != nil {
			return nil, err
		}
		env[k] = v
	}

	return env, nil
}

func Environ(e []string, path string) (env []string, err error) {
	kv, err := containerEnvs(path)
	if err != nil {
		return nil, err
	}
	for _, l := range e {
		k, v, err := parseEnv(l)
		if err != nil {
			return nil, err
		}
		kv[k] = v
	}
	for k, v := range kv {
		env = append(env, k + "=" + v)
	}
	return env, nil
}
