package gitio

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"hash"
	"io"
)

// HashfileReader reads and verifies Git "hashfiles" as defined in
// https://github.com/git/git/blob/master/csum-file.h. The hash algorithm
// is hard-coded to SHA1.
type HashfileReader struct {
	tr  *TrailerReader
	tee io.Reader
	sum hash.Hash
}

// NewHashfileReader wraps r to return a reader that will omit the
// trailing checksum. When the HashfileReader reaches EOF, it will
// transparently compare the against the trailing checksum provided by r.
func NewHashfileReader(r io.Reader) *HashfileReader {
	sum := sha1.New()
	tr := NewTrailerReader(r, sum.Size())
	return &HashfileReader{
		tr:  tr,
		tee: io.TeeReader(tr, sum),
		sum: sum,
	}
}

func (hr *HashfileReader) Read(p []byte) (int, error) {
	n, err := hr.tee.Read(p)
	if err == io.EOF {
		return n, hr.validateChecksum()
	}

	return n, err
}

func (hr *HashfileReader) validateChecksum() error {
	trailer, err := hr.tr.Trailer()
	if err != nil {
		return err
	}

	if actualSum := hr.sum.Sum(nil); !bytes.Equal(trailer, actualSum) {
		return fmt.Errorf("hashfile checksum mismatch: expected %x got %x", trailer, actualSum)
	}

	return io.EOF
}
