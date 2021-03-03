package pktline

// Utility functions for working with the Git pkt-line format. See
// https://github.com/git/git/blob/master/Documentation/technical/protocol-common.txt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"sync"
)

const (
	maxPktSize = 65520 // https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/pkt-line.h#L216
	pktDelim   = "0001"
)

// NewScanner returns a bufio.Scanner that splits on Git pktline boundaries
func NewScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, maxPktSize), maxPktSize)
	scanner.Split(pktLineSplitter)
	return scanner
}

// Data returns the packet pkt without its length header. The length
// header is not validated. Returns an empty slice when pkt is a magic packet such
// as '0000'.
func Data(pkt []byte) []byte {
	return pkt[4:]
}

// IsFlush detects the special flush packet '0000'
func IsFlush(pkt []byte) bool {
	return bytes.Equal(pkt, PktFlush())
}

// WriteString writes a string with pkt-line framing
func WriteString(w io.Writer, str string) (int, error) {
	pktLen := len(str) + 4
	if pktLen > maxPktSize {
		return 0, fmt.Errorf("string too large: %d bytes", len(str))
	}

	_, err := fmt.Fprintf(w, "%04x%s", pktLen, str)
	return len(str), err
}

// WriteFlush writes a pkt flush packet.
func WriteFlush(w io.Writer) error {
	_, err := w.Write(PktFlush())
	return err
}

// WriteDelim writes a pkt delim packet.
func WriteDelim(w io.Writer) error {
	_, err := fmt.Fprint(w, pktDelim)
	return err
}

// PktDone returns the bytes for a "done" packet.
func PktDone() []byte {
	return []byte("0009done\n")
}

// PktFlush returns the bytes for a "flush" packet.
func PktFlush() []byte {
	return []byte("0000")
}

func pktLineSplitter(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if len(data) < 4 {
		if atEOF && len(data) > 0 {
			return 0, nil, fmt.Errorf("pktLineSplitter: incomplete length prefix on %q", data)
		}
		return 0, nil, nil // want more data
	}

	// We have at least 4 bytes available so we can decode the 4-hex digit
	// length prefix of the packet line.
	pktLength64, err := strconv.ParseInt(string(data[:4]), 16, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("pktLineSplitter: decode length: %v", err)
	}

	// Cast is safe because we requested an int-size number from strconv.ParseInt
	pktLength := int(pktLength64)

	if pktLength < 0 {
		return 0, nil, fmt.Errorf("pktLineSplitter: invalid length: %d", pktLength)
	}

	if pktLength < 4 {
		// Special case: magic empty packet 0000, 0001, 0002 or 0003.
		return 4, data[:4], nil
	}

	if len(data) < pktLength {
		// data contains incomplete packet

		if atEOF {
			return 0, nil, fmt.Errorf("pktLineSplitter: less than %d bytes in input %q", pktLength, data)
		}

		return 0, nil, nil // want more data
	}

	return pktLength, data[:pktLength], nil
}

// SidebandWriter multiplexes byte streams into a single sideband64k stream.
type SidebandWriter struct {
	W io.Writer
	m sync.Mutex
}

func (sw *SidebandWriter) writeBand(band byte, p []byte) (int, error) {
	sw.m.Lock()
	defer sw.m.Unlock()

	n := 0
	for len(p) > 0 {
		m := len(p)
		const headerSize = 5
		if max := maxPktSize - headerSize; m > max {
			m = max
		}

		if _, err := fmt.Fprintf(sw.W, "%04x%s", m+headerSize, []byte{band}); err != nil {
			return n, err
		}

		if _, err := sw.W.Write(p[:m]); err != nil {
			return n, err
		}
		p = p[m:]
		n += m
	}

	return n, nil
}

// Writer returns an io.Writer that writes into the multiplexed stream.
// Writers for different bands can be used concurrently.
func (sw *SidebandWriter) Writer(band byte) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		return sw.writeBand(band, p)
	})
}

type writerFunc func([]byte) (int, error)

func (wf writerFunc) Write(p []byte) (int, error) { return wf(p) }

// EachSidebandPacket iterates over a sideband64k pktline stream. For
// each packet, it will call fn with the band ID and the packet. Fn must
// not retain the packet.
func EachSidebandPacket(r io.Reader, fn func(byte, []byte) error) error {
	scanner := NewScanner(r)

	for scanner.Scan() {
		data := Data(scanner.Bytes())
		if len(data) == 0 {
			return fmt.Errorf("invalid sideband packet: %q", scanner.Text())
		}
		if err := fn(data[0], data[1:]); err != nil {
			return err
		}
	}

	return scanner.Err()
}
