package log

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git"
)

// Parser holds necessary state for parsing a git log stream
type Parser struct {
	reader        *bufio.Reader
	currentCommit *pb.GitCommit
	finished      bool
	err           error
	*commitMessageHelper
}

const fieldDelimiter = "\x1f"

// NewLogParser returns a new Parser
func NewLogParser(ctx context.Context, repo *pb.Repository, src io.Reader) (*Parser, error) {
	cmh, err := newCommitMessageHelper(ctx, repo)
	if err != nil {
		return nil, err
	}

	parser := &Parser{
		reader:              bufio.NewReader(src),
		commitMessageHelper: cmh,
	}

	return parser, nil
}

// Parse parses a single git log line. It returns true if successful, false if it finished
// parsing all logs or when it encounters an error, in which case use Parser.Err()
// to get the error.
func (parser *Parser) Parse() bool {
	if parser.finished {
		return false
	}

	line, err := parser.reader.ReadBytes('\x00')
	if err != nil && err != io.EOF {
		parser.err = err
	} else if err == io.EOF {
		parser.finished = true
	}

	if len(line) == 0 {
		return false
	}

	if line[len(line)-1] == '\x00' {
		line = line[:len(line)-1] // strip off the null byte
	}

	elements := bytes.Split(line, []byte(fieldDelimiter))
	if len(elements) != len(commitLogFormatFields) {
		parser.err = fmt.Errorf("error parsing ref: %q", line)
		return false
	}

	subject, body, err := parser.commitMessage(string(elements[0]))
	if err != nil {
		parser.err = err
		return false
	}

	var parentIds []string
	if parentFieldIndex := len(commitLogFormatFields) - 1; len(elements[parentFieldIndex]) > 0 {
		parentIds = strings.Split(string(elements[parentFieldIndex]), " ")
	}

	commit, err := git.NewCommit(elements[0], []byte(subject), []byte(body),
		elements[1], elements[2], elements[3], elements[4], elements[5],
		elements[6], parentIds...)
	if err != nil {
		parser.err = err
		return false
	}

	parser.currentCommit = commit
	return true
}

// Commit returns a successfully parsed git log line. It should be called only when Parser.Parse()
// returns true.
func (parser *Parser) Commit() *pb.GitCommit {
	return parser.currentCommit
}

// Err returns the error encountered (if any) when parsing the diff stream. It should be called only when Parser.Parse()
// returns false.
func (parser *Parser) Err() error {
	return parser.err
}
