package archive

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/pkg/archive"

	"github.com/yuuki1/droot/osutil"
)

const compressionBufSize = 32768

var RsyncDefaultOpts = []string{"-av", "--delete"}

func ExtractTarGz(in io.Reader, dest string, uid int, gid int) (err error) {
	// If dest dir doesn't exists, create it and chown uid/gid
	if !osutil.ExistsDir(dest) {
		if err := os.Mkdir(dest, 0755); err != nil {
			return err
		}
		os.Chown(dest, uid, gid)
	}

	nolchown := true
	if uid < 0 {
		nolchown = false
	}
	if gid < 0 {
		nolchown = false
	}

	return archive.Untar(in, dest, &archive.TarOptions{
		Compression: archive.Gzip,
		NoLchown: nolchown,
		ChownOpts: &archive.TarChownOptions{
			UID: uid,
			GID: gid,
		},
	})
}

func Rsync(from, to string, arg ...string) error {
	from = from + "/"
	// append "/" when not terminated by "/"
	if strings.LastIndex(to, "/") != len(to)-1 {
		to = to + "/"
	}

	// TODO --exclude, --excluded-from
	rsyncArgs := []string{}
	rsyncArgs = append(rsyncArgs, RsyncDefaultOpts...)
	rsyncArgs = append(rsyncArgs, from, to)
	if err := osutil.RunCmd("rsync", rsyncArgs...); err != nil {
		return err
	}

	return nil
}

func Compress(in io.Reader) io.ReadCloser {
	pReader, pWriter := io.Pipe()
	bufWriter := bufio.NewWriterSize(pWriter, compressionBufSize)
	compressor := gzip.NewWriter(bufWriter)

	go func() {
		_, err := io.Copy(compressor, in)
		if err == nil {
			err = compressor.Close()
		}
		if err == nil {
			err = bufWriter.Flush()
		}
		if err != nil {
			pWriter.CloseWithError(err)
		} else {
			pWriter.Close()
		}
	}()

	return pReader
}

