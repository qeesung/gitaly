package lines

import (
	"bufio"
	"bytes"
	"io"
)

// ItemsPerMessage establishes the threshold to flush the buffer when using the
// `Send` function. It's a variable instead of a constant to make it possible to
// override in tests.
var ItemsPerMessage = 20

// Sender handles a buffer of lines from a Git command
type Sender func([][]byte) error

type writer struct {
	sender Sender
	lines  [][]byte
	delim  []byte
}

// CopyAndAppend adds a newly allocated copy of `e` to the `s` slice. Useful to
// avoid io buffer shennanigans
func CopyAndAppend(s [][]byte, e []byte) [][]byte {
	line := make([]byte, len(e))
	copy(line, e)
	return append(s, line)
}

// flush calls the `sender` handler function with the accumulated lines and
// clears the lines buffer.
func (w *writer) flush() error {
	if len(w.lines) == 0 { // No message to send, just return
		return nil
	}

	if err := w.sender(w.lines); err != nil {
		return err
	}

	// Reset the message
	w.lines = nil

	return nil
}

// addLine adds a new line to the writer buffer, and flushes if the maximum
// size has been achieved
func (w *writer) addLine(p []byte) error {
	w.lines = CopyAndAppend(w.lines, p)

	if len(w.lines) >= ItemsPerMessage {
		return w.flush()
	}

	return nil
}

// consume reads from an `io.Reader` and writes each line to the buffer. It
// flushes after being done reading.
func (w *writer) consume(r io.Reader) error {
	buf := bufio.NewReader(r)

	for finished := false; !finished; {
		var line []byte

		for {
			// delim can be multiple bytes, so we read till the end byte of it ...
			chunk, err := buf.ReadBytes(w.delim[len(w.delim)-1])
			if err != nil && err != io.EOF {
				return err
			}

			line = append(line, chunk...)
			// ... then we check if the last bytes of line are the same as delim
			if bytes.HasSuffix(line, w.delim) {
				break
			}

			if err == io.EOF {
				finished = true
				break
			}
		}

		line = bytes.TrimRight(line, string(w.delim))
		if len(line) == 0 {
			break
		}

		if err := w.addLine(line); err != nil {
			return err
		}
	}

	return w.flush()
}

// Send reads output from `r`, splits it at `delim`, then handles the buffered lines using `sender`.
func Send(r io.Reader, sender Sender, delim []byte) error {
	if len(delim) == 0 {
		delim = []byte{'\n'}
	}

	writer := &writer{sender: sender, delim: delim}
	return writer.consume(r)
}
