package pktline

// Utility functions for working with the Git pkt-line format. See
// https://github.com/git/git/blob/master/Documentation/technical/protocol-common.txt

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

const (
	maxPktSize = 0xffff
)

// NewScanner returns a bufio.Scanner that splits on Git pktline boundaries
func NewScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, maxPktSize), maxPktSize)
	scanner.Split(pktLineSplitter)
	return scanner
}

// Data returns the data contained in pkt. The length prefix is not validated.
func Data(pkt []byte) []byte {
	if len(pkt) == 4 {
		return nil
	}

	return pkt[4:]
}

// IsMagic detects magic packets such as '0000'
func IsMagic(pkt []byte) bool {
	return len(pkt) == 4 && bytes.HasPrefix(pkt, []byte("000")) && pkt[3] < '4' && pkt[3] >= '0'
}

func pktLineSplitter(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if len(data) < 4 {
		if atEOF && len(data) > 0 {
			return 0, nil, fmt.Errorf("pktLineSplitter: incomplete length prefix on %q", data)
		}
		return 0, nil, nil // want more data
	}

	// Invariant: len(data) >= 4

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

	// Invariant: len(data) >= 4, pktLength >= 4

	if len(data) < pktLength {
		// data contains incomplete packet

		if atEOF {
			return 0, nil, fmt.Errorf("pktLineSplitter: less than %d bytes in input %q", pktLength, data)
		}

		return 0, nil, nil // want more data
	}

	return pktLength, data[:pktLength], nil
}
