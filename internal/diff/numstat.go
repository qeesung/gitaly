package diff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// NumStat represents a single parsed diff file change
type NumStat struct {
	Path      []byte
	OldPath   []byte
	Additions int32
	Deletions int32
}

// NumStatParser holds necessary state for parsing the numstat output
type NumStatParser struct {
	reader *bufio.Reader
}

const (
	numStatDelimiter = 0
)

// NewDiffNumStatParser returns a new NumStatParser
func NewDiffNumStatParser(src io.Reader) *NumStatParser {
	parser := &NumStatParser{}
	reader := bufio.NewReader(src)
	parser.reader = reader

	return parser
}

// NextNumStat reads from git diff --numstat -z command,
// parses the stats and returns a *NumStat.
func (parser *NumStatParser) NextNumStat() (*NumStat, error) {
	result := &NumStat{}

	data, err := parser.reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}

	// We expect each `data` to be <NUM_ADDED>\t<NUM_DELETED>\t<REST>\0
	// <REST> can be either "<PATH>\0" or just "\0"
	// In the latter case we are dealing with a rename (see below).
	split := bytes.SplitN(data, []byte("\t"), 3)
	if len(split) != 3 {
		return nil, fmt.Errorf("error parsing %q", data)
	}

	result.Additions, err = convertNumStat(split[0])
	if err != nil {
		return nil, err
	}

	result.Deletions, err = convertNumStat(split[1])
	if err != nil {
		return nil, err
	}

	rest := split[2]
	if len(rest) == 0 {
		return nil, fmt.Errorf("error parsing %q", data)
	}

	if !bytes.Equal(rest, []byte{numStatDelimiter}) {
		// We know that the last byte in 'rest' is a zero byte because of the
		// contract of bufio.Reader.ReadBytes.
		result.Path = rest[:len(rest)-1]
		return result, nil
	}

	// We are in the rename case. There will be two more zero-terminated
	// strings: old path and new path.
	oldPath, err := parser.reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}

	newPath, err := parser.reader.ReadBytes(numStatDelimiter)
	if err != nil {
		return nil, err
	}

	// Discard trailing zero byte left by ReadBytes
	result.OldPath = oldPath[:len(oldPath)-1]
	result.Path = newPath[:len(newPath)-1]
	return result, nil
}

func convertNumStat(num []byte) (int32, error) {
	// It's a binary numstat
	if bytes.Equal(num, []byte("-")) {
		return 0, nil
	}

	parsedNum, err := strconv.ParseInt(string(num), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("error converting diff num stat: %v", err)
	}

	return int32(parsedNum), nil
}
