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
	// MaxSidebandData is the maximum number of bytes that fits into one Git
	// pktline side-band-64k packet.
	MaxSidebandData = MaxPktSize - 5

	// MaxPktSize is the maximum size of content of a Git pktline side-band-64k
	// packet, excluding size of length and band number
	// https://gitlab.com/gitlab-org/git/-/blob/v2.30.0/pkt-line.h#L216
	MaxPktSize = 65520
	pktDelim   = "0001"
)

// NewScanner returns a bufio.Scanner that splits on Git pktline boundaries
func NewScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, MaxPktSize), MaxPktSize)
	scanner.Split(pktLineSplitter)
	return scanner
}

// Data returns the packet pkt without its length header. The length
// header is not validated. Returns an empty slice when pkt is a magic packet such
// as '0000'.
func Data(pkt []byte) []byte {
	return pkt[4:]
}

// Payload returns the pktline's data. It verifies that the length header matches what we expect as
// data.
func Payload(pkt []byte) ([]byte, error) {
	if len(pkt) < 4 {
		return nil, fmt.Errorf("packet too small")
	}

	if IsFlush(pkt) {
		return nil, fmt.Errorf("flush packets do not have a payload")
	}

	lengthHeader := string(pkt[:4])
	length, err := strconv.ParseUint(lengthHeader, 16, 16)
	if err != nil {
		return nil, fmt.Errorf("parsing length header %q: %w", lengthHeader, err)
	}

	if uint64(len(pkt)) != length {
		return nil, fmt.Errorf("packet length %d does not match header length %d", len(pkt), length)
	}

	return pkt[4:], nil
}

// IsFlush detects the special flush packet '0000'
func IsFlush(pkt []byte) bool {
	return bytes.Equal(pkt, PktFlush())
}

// WriteString writes a string with pkt-line framing
func WriteString(w io.Writer, str string) (int, error) {
	pktLen := len(str) + 4
	if pktLen > MaxPktSize {
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
			return 0, nil, io.ErrUnexpectedEOF
		}

		return 0, nil, nil // want more data
	}

	return pktLength, data[:pktLength], nil
}

// SidebandWriter multiplexes byte streams into a single side-band-64k stream.
type SidebandWriter struct {
	w   io.Writer
	m   sync.Mutex
	buf [MaxPktSize]byte // Use a buffer to coalesce header and payload into one write syscall
}

// NewSidebandWriter instantiates a new SidebandWriter.
func NewSidebandWriter(w io.Writer) *SidebandWriter { return &SidebandWriter{w: w} }

func (sw *SidebandWriter) writeBand(band byte, data []byte) (int, error) {
	sw.m.Lock()
	defer sw.m.Unlock()

	n := 0
	for len(data) > 0 {
		const headerSize = 5

		chunkSize := copy(sw.buf[headerSize:], data)
		header := chunkSize + headerSize
		copy(sw.buf[:4], fmt.Sprintf("%04x", header))
		sw.buf[4] = band

		if _, err := sw.w.Write(sw.buf[:header]); err != nil {
			return n, err
		}
		data = data[chunkSize:]
		n += chunkSize
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

type errNotSideband struct{ pkt string }

func (err *errNotSideband) Error() string { return fmt.Sprintf("invalid sideband packet: %q", err.pkt) }

// EachSidebandPacket iterates over a side-band-64k pktline stream. For
// each packet, it will call fn with the band ID and the packet. Fn must
// not retain the packet.
func EachSidebandPacket(r io.Reader, fn func(byte, []byte) error) error {
	scanner := NewScanner(r)

	for scanner.Scan() {
		data := Data(scanner.Bytes())
		if len(data) == 0 {
			return &errNotSideband{scanner.Text()}
		}
		if err := fn(data[0], data[1:]); err != nil {
			return err
		}
	}

	return scanner.Err()
}
