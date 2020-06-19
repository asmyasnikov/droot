package environ

import (
	"sort"
	"testing"

	"github.com/kylelemons/godebug/pretty"
)

func TestGetEnvironFromEnvFile(t *testing.T) {
	env, err := containerEnvs("../testdata/drootenv")
	if err != nil {
		t.Errorf("should not be error: %v", err)
	}
	expected := map[string]string{
		"HOME":"/root",
		"GOLANG_DOWNLOAD_SHA256":"5470eac05d273c74ff8bac7bef5bad0b5abbd1c4052efbdbc8db45332e836b0b",
		"PATH":"/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOPATH":"/go",
		"PWD":"/go",
		"GOLANG_DOWNLOAD_URL":"https://golang.org/dl/go1.6.linux-amd64.tar.gz",
		"GOLANG_VERSION":"1.6",
	}
	if diff := pretty.Compare(env, expected); diff != "" {
		t.Fatalf("diff: (-actual +expected)\n%s", diff)
	}
}

func TestEnviron(t *testing.T) {
	env, err := Environ([]string{
		"HOME=/home/user",
		"GOPATH=/home/user/go",
	},
		"../testdata/drootenv",
	)
	if err != nil {
		t.Errorf("should not be error: %v", err)
	}
	sort.Strings(env)
	expected := []string{
		"HOME=/home/user",
		"GOLANG_DOWNLOAD_SHA256=5470eac05d273c74ff8bac7bef5bad0b5abbd1c4052efbdbc8db45332e836b0b",
		"PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOPATH=/home/user/go",
		"PWD=/go",
		"GOLANG_DOWNLOAD_URL=https://golang.org/dl/go1.6.linux-amd64.tar.gz",
		"GOLANG_VERSION=1.6",
	}
	sort.Strings(expected)
	if diff := pretty.Compare(env, expected); diff != "" {
		t.Fatalf("diff: (-actual +expected)\n%s", diff)
	}
}
