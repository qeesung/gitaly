package helper

import (
	"bytes"
	"io"
)

// CountingWriter wraps an io.Writer and counts all the writes. Accessing
// the count N is not thread-safe.
type CountingWriter struct {
	W io.Writer
	N int64
	b bytes.Buffer
}

func (cw *CountingWriter) Write(p []byte) (int, error) {
	n, err := cw.W.Write(p)
	cw.N += int64(n)
	cw.b.Write(p)
	return n, err
}

func (cw *CountingWriter) String() string {
	return cw.b.String()
}
