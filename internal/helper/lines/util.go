package lines

import (
	"bufio"
	"bytes"
)

// ScanWithDelimiter generates a `bufio.SplitFunc` that uses `delim` as the
// delimiter. Based on `bufio.ScanLines` https://golang.org/src/bufio/scan.go?s=11488:11566#L329
func ScanWithDelimiter(delim []byte) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.Index(data, delim); i >= 0 {
			// We have a full delim-terminated line.
			return i + len(delim), data[0:i], nil
		}
		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), data, nil
		}
		// Request more data.
		return 0, nil, nil
	}
}
