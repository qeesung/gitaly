package smarthttp

import (
	"bytes"
	"io"
	"io/ioutil"

	"gitlab.com/gitlab-org/gitaly/internal/git/pktline"
)

func scanDeepen(body io.Reader) bool {
	result := false

	scanner := pktline.NewScanner(body)
	for scanner.Scan() {
		if bytes.HasPrefix(pktline.Data(scanner.Bytes()), []byte("deepen")) && scanner.Err() == nil {
			result = true
			break
		}
	}

	// Because we are connected to another consumer via an io.Pipe and
	// io.TeeReader we must consume all data.
	io.Copy(ioutil.Discard, body)
	return result
}
