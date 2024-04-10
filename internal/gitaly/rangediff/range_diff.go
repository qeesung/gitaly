package rangediff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const (
	// RangeDiffCommitPairRegex is the regex to match a commit pair line in the range-diff output
	RangeDiffCommitPairRegex = `^(-|(\d+)): +(-+|([a-f0-9]+)) ([>=!<]) (-|(\d+)): +(-+|([a-f0-9]+)) (.*)$`
	// PatchDataPrefixWhiteSpaceLength is the length of the prefix white spaces in the patch data line
	PatchDataPrefixWhiteSpaceLength = 4
)

var commitPairRegex = regexp.MustCompile(RangeDiffCommitPairRegex)

// CommitPair is the result from parsing a range diff "commit pair line"
type CommitPair struct {
	FromCommit         string
	ToCommit           string
	Comparison         gitalypb.RangeDiffResponse_Comparator
	CommitMessageTitle string
	PatchData          []byte
}

// Parser holds necessary state for parsing a range-diff stream
type Parser struct {
	err               error
	finished          bool
	patchReader       *bufio.Reader
	currentCommitPair *CommitPair
	nextCommitPair    *CommitPair
}

// NewRangeDiffParser returns a new Parser
// The output of `git range-diff` consists of multiple "commit pair lines".
// Each "commit pair line" consists of several parts,
// for example with a "commit pair line" like "2: f00dbal ! 3: decafe1 Describe a bug":
// where:
//
// * "f00dbal" is the `from_commit_id`,
// * "decafe1" is the `to_commit_id`,
// * "Describe a bug" is the `commit_message_title`,
// * "!" is the `comparison`.
//
// And if there is patch diff data following a commit pair line,
// it will also parse that patch diff data as `patch_data`.
//
// Specifically, when the `comparison` is ">", `from_commit_id` equals "-------";
// conversely, when the `comparison` is "<", `to_commit_id` equals "-------".
func NewRangeDiffParser(src io.Reader) *Parser {
	return &Parser{
		patchReader: bufio.NewReader(src),
	}
}

// Parse reads the range-diff stream and returns true if a commit pair line was successfully parsed, false if the stream is finished
func (parser *Parser) Parse() bool {
	if parser.finished {
		// In case we didn't consume the whole output due to reaching limitations
		_, _ = io.Copy(io.Discard, parser.patchReader)
		return false
	}

	parser.initializeCurrentCommitPair()

	for {
		line, err := parser.patchReader.ReadBytes('\n')
		if err != nil {
			parser.finished = true

			if err != io.EOF {
				parser.err = fmt.Errorf("reading patch data: %w", err)
				return false
			}

			if len(line) == 0 {
				return parser.currentCommitPair != nil
			}
		}

		line = bytes.TrimSuffix(line, []byte("\n"))

		if commitPairRegex.Match(line) {
			// mach the commit pair line
			matches := commitPairRegex.FindSubmatch(line)

			comparison, err := ParseCompareSymbol(string(matches[5]))
			if err != nil {
				parser.err = fmt.Errorf("parsing compare symbol: %w", err)
				return false
			}
			commitPair := &CommitPair{
				FromCommit:         string(matches[3]),
				Comparison:         comparison,
				ToCommit:           string(matches[8]),
				CommitMessageTitle: string(matches[10]),
			}

			// keep the parsed commit pair for the next call to Parse()
			if parser.currentCommitPair != nil {
				parser.nextCommitPair = commitPair
				return true
			}
			parser.currentCommitPair = commitPair
			if comparison != gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL {
				return true
			}
			// only ! need to continue to read patch data
			continue
		} else if parser.currentCommitPair == nil {
			parser.err = fmt.Errorf("range-diff commit pair not found, current line: %s", line)
			parser.finished = true
			return false
		} else if len(line) < PatchDataPrefixWhiteSpaceLength {
			parser.err = fmt.Errorf("range-diff patch data too short, line size: %d", len(line))
			parser.finished = true
			return false
		}
		// match the patch data line
		parser.currentCommitPair.PatchData = append(parser.currentCommitPair.PatchData, line[PatchDataPrefixWhiteSpaceLength:]...)
		parser.currentCommitPair.PatchData = append(parser.currentCommitPair.PatchData, '\n')
	}
}

// Err returns the error encountered (if any) when parsing the range-diff stream. It should be called only when Parser.Parse()
// returns false.
func (parser *Parser) Err() error {
	return parser.err
}

// CommitPair returns a successfully parsed range-diff commit pair. It should be called only when Parser.Parse()
// returns true. The return value is valid only until the next call to Parser.Parse().
func (parser *Parser) CommitPair() *CommitPair {
	return parser.currentCommitPair
}

func (parser *Parser) initializeCurrentCommitPair() {
	if parser.nextCommitPair != nil {
		parser.currentCommitPair = parser.nextCommitPair
		parser.nextCommitPair = nil
	} else {
		parser.currentCommitPair = nil
	}
}

// ParseCompareSymbol parse the compare symbol to protobuf RangeDiffResponse_Comparator ! = > <
func ParseCompareSymbol(symbol string) (gitalypb.RangeDiffResponse_Comparator, error) {
	switch symbol {
	case "=":
		return gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED, nil
	case ">":
		return gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN, nil
	case "<":
		return gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN, nil
	case "!":
		return gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL, nil
	default:
		return gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED, fmt.Errorf("invalid compare symbol: %s", symbol)
	}
}

// ParseCompareSymbolToString parse the protobuf RangeDiffResponse_Comparator to string symbol ! = > <
func ParseCompareSymbolToString(comparator gitalypb.RangeDiffResponse_Comparator) string {
	switch comparator {
	case gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED:
		return "="
	case gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN:
		return ">"
	case gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN:
		return "<"
	case gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL:
		return "!"
	default:
		return "="
	}
}
