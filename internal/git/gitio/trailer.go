package gitio

import (
	"fmt"
	"io"
)

// TrailerReader models the behavior of Git hashfiles where the last N
// bytes of the underlying reader are not part of the content.
// TrailerReader acts like an io.Reader but will always hold back the
// last N bytes. Once the underlying reader has reached EOF, the trailer
// (the last N bytes) can be retrieved with the Trailer() method.
type TrailerReader struct {
	r           io.Reader
	start, end  int
	trailerSize int
	buf         []byte
	atEOF       bool
}

// NewTrailerReader returns a new TrailerReader. The returned
// TrailerReader will never return the last trailerSize bytes of r; to
// get to those bytes, first read the TrailerReader to EOF and then call
// Trailer().
func NewTrailerReader(r io.Reader, trailerSize int) *TrailerReader {
	const bufSize = 8192
	if trailerSize >= bufSize {
		panic("trailerSize too large for TrailerReader")
	}

	return &TrailerReader{
		r:           r,
		trailerSize: trailerSize,
		buf:         make([]byte, bufSize),
	}
}

// Trailer yields the last trailerSize bytes of the underlying reader of
// tr. If the underlying reader has not reached EOF yet Trailer will
// return an error.
func (tr *TrailerReader) Trailer() ([]byte, error) {
	bufLen := tr.end - tr.start
	if !tr.atEOF || bufLen > tr.trailerSize {
		return nil, fmt.Errorf("cannot get trailer before reader has reached EOF")
	}

	if bufLen < tr.trailerSize {
		return nil, fmt.Errorf("not enough bytes to yield trailer")
	}

	return tr.buf[tr.end-tr.trailerSize : tr.end], nil
}

func (tr *TrailerReader) Read(p []byte) (int, error) {
	if bufLen := tr.end - tr.start; !tr.atEOF && bufLen <= tr.trailerSize {
		copy(tr.buf, tr.buf[tr.start:tr.end])
		tr.start = 0
		tr.end = bufLen

		n, err := tr.r.Read(tr.buf[tr.end:])
		if err != nil {
			if err != io.EOF {
				return 0, err
			}
			tr.atEOF = true
		}
		tr.end += n
	}

	if tr.end-tr.start <= tr.trailerSize {
		if tr.atEOF {
			return 0, io.EOF
		}
		return 0, nil
	}

	n := copy(p, tr.buf[tr.start:tr.end-tr.trailerSize])
	tr.start += n
	return n, nil
}
