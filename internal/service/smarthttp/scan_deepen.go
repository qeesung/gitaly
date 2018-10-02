package smarthttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
)

func scanDeepen(body io.Reader) bool {
	result := false

	scanner := bufio.NewScanner(body)
	scanner.Split(pktLineSplitter)
	for scanner.Scan() {
		if bytes.HasPrefix(scanner.Bytes(), []byte("deepen")) && scanner.Err() == nil {
			result = true
			break
		}
	}

	// Because we are connected to another consumer via an io.Pipe and
	// io.TeeReader we must consume all data.
	io.Copy(ioutil.Discard, body)
	return result
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
		return 4, data[:0], nil
	}

	// Invariant: len(data) >= 4, pktLength >= 4

	if len(data) < pktLength {
		// data contains incomplete packet

		if atEOF {
			return 0, nil, fmt.Errorf("pktLineSplitter: less than %d bytes in input %q", pktLength, data)
		}

		return 0, nil, nil // want more data
	}

	// return "pkt" token without length prefix
	return pktLength, data[4:pktLength], nil
}
