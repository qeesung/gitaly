package text

import (
	"bytes"
)

// ChompBytes converts b to a string with its trailing newline, if present, removed.
func ChompBytes(b []byte) string {
	return string(bytes.TrimSuffix(b, []byte{'\n'}))
}
