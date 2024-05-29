package conflict

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
)

// section denotes the various conflict sections
type section uint

const (
	// Resolutions for conflict is done by clients providing selections for
	// the different conflict sections. "head"/"origin" is used to denote the
	// selection for ours/their trees respectively.
	head   = "head"
	origin = "origin"

	// fileLimit is used to set the limit on the buffer size for bufio.Scanner,
	// we don't support conflict resolution for files which require bigger buffers.
	fileLimit = 200 * (1 << 10)
)

const (
	// The sections are used to define various lines of the conflicted file.
	// Here Old/New is used to denote ours/their respectively.
	sectionNone = section(iota)
	sectionOld
	sectionNew
	sectionNoNewline
)

// Errors that can occur during parsing of a merge conflict file
var (
	// ErrUnmergeableFile is returned when the either the file exceeds the
	// fileLimit or no data was read from the file (in case of a binary file).
	ErrUnmergeableFile = errors.New("merging is not supported for file")
	// ErrUnexpectedDelimiter is returned  when the previous section doesn't
	// match the expected flow of sections.
	ErrUnexpectedDelimiter = errors.New("unexpected conflict delimiter")
	// ErrMissingEndDelimiter is returned when the final section parsed doesn't
	// match the expected end state.
	ErrMissingEndDelimiter = errors.New("missing last delimiter")
)

// line is a structure used to denote individual lines in the conflicted blob,
// with information around how that line maps to ours/theirs blobs.
type line struct {
	// objIndex states the cursor position in the conflicted blob
	objIndex uint
	// oldIndex states the cursor position in the 'ours' blob
	oldIndex uint
	// oldIndex states the cursor position in the 'theirs' blob
	newIndex uint
	// payload denotes the content of line (sans the newline)
	payload string
	// crlf indicates if the line uses a CRLF line break.
	crlf bool
	// section denotes which section this line belongs to.
	section section
}

// Resolution indicates how to resolve a conflict
type Resolution struct {
	// OldPath is the mapping of the path wrt to 'ours' OID
	OldPath string `json:"old_path"`
	// OldPath is the mapping of the path wrt to 'their' OID
	NewPath string `json:"new_path"`

	// Sections is a map which is used to denote which section to select
	// for each conflict. Key is the sectionID, while the value is either
	// "head" or "origin", which denotes the ours/theirs OIDs respectively.
	Sections map[string]string `json:"sections"`

	// Content is used when no sections are defined
	Content string `json:"content"`
}

// Resolve is used to resolve conflicts for a given blob. It expects the blob
// to be provided as an io.Reader along with the resolutions for the provided
// blob. Clients can also use appendNewLine to have an additional new line appended
// to the end of the resolved buffer.
func Resolve(src io.Reader, ours, theirs git.ObjectID, path string, resolution Resolution, appendNewLine bool) (io.Reader, error) {
	var (
		// conflict markers, git-merge-tree(1) appends the tree OIDs to the markers
		start  = "<<<<<<< " + ours.String()
		middle = "======="
		end    = ">>>>>>> " + theirs.String()

		objIndex, oldIndex, newIndex uint = 0, 1, 1
		currentSection               section
		bytesRead                    int
		resolvedContent              bytes.Buffer

		s = bufio.NewScanner(src)
	)

	// When the paths are different, the conflicts contain the path names.
	if resolution.OldPath != resolution.NewPath {
		start = fmt.Sprintf("%s:%s", start, resolution.OldPath)
		end = fmt.Sprintf("%s:%s", end, resolution.NewPath)
	}

	// allow for line scanning up to the file limit
	s.Buffer(make([]byte, 4096), fileLimit)

	s.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		defer func() { bytesRead += advance }()

		if bytesRead >= fileLimit {
			return 0, nil, ErrUnmergeableFile
		}

		// The remaining function is a modified version of
		// bufio.ScanLines that does not consume carriage returns
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			// We have a full newline-terminated line.
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})

	var lines []line

	for s.Scan() {
		switch l, crlf := strings.CutSuffix(s.Text(), "\r"); l {
		case start:
			if currentSection != sectionNone {
				return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrUnexpectedDelimiter)
			}
			currentSection = sectionNew
		case middle:
			if currentSection != sectionNew {
				return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrUnexpectedDelimiter)
			}
			currentSection = sectionOld
		case end:
			if currentSection != sectionOld {
				return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrUnexpectedDelimiter)
			}
			currentSection = sectionNone
		default:
			if len(l) > 0 && l[0] == '\\' {
				currentSection = sectionNoNewline
				lines = append(lines, line{
					objIndex: objIndex,
					oldIndex: oldIndex,
					newIndex: newIndex,
					payload:  l,
					crlf:     crlf,
					section:  currentSection,
				})
				continue
			}
			lines = append(lines, line{
				objIndex: objIndex,
				oldIndex: oldIndex,
				newIndex: newIndex,
				payload:  l,
				crlf:     crlf,
				section:  currentSection,
			})

			objIndex++
			if currentSection != sectionNew {
				oldIndex++
			}
			if currentSection != sectionOld {
				newIndex++
			}
		}
	}

	if err := s.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrUnmergeableFile)
		}
		return &resolvedContent, err
	}

	if currentSection == sectionOld || currentSection == sectionNew {
		return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrMissingEndDelimiter)
	}

	if bytesRead == 0 {
		return &resolvedContent, fmt.Errorf("resolve: parse conflict for %q: %w", path, ErrUnmergeableFile) // typically a binary file
	}

	var sectionID string

	if len(resolution.Sections) == 0 {
		resolvedContent.Write([]byte(resolution.Content))

		return &resolvedContent, nil
	}

	resolvedLines := make([]string, 0, len(lines))
	for _, l := range lines {
		if l.section == sectionNone {
			sectionID = ""
			resolvedLines = append(resolvedLines, l.payload)
			continue
		}

		if sectionID == "" {
			sectionID = fmt.Sprintf("%x_%d_%d", sha1.Sum([]byte(path)), l.oldIndex, l.newIndex)
		}

		r, ok := resolution.Sections[sectionID]
		if !ok {
			return nil, fmt.Errorf("Missing resolution for section ID: %s", sectionID) //nolint
		}

		switch r {
		case head:
			if l.section != sectionNew {
				continue
			}
		case origin:
			if l.section != sectionOld {
				continue
			}
		default:
			return nil, fmt.Errorf("Missing resolution for section ID: %s", sectionID) //nolint
		}

		resolvedLines = append(resolvedLines, l.payload)
	}

	lineBreak := "\n"
	if len(lines) > 0 && lines[0].crlf {
		lineBreak = "\r\n"
	}

	resolvedContent.Write([]byte(strings.Join(resolvedLines, lineBreak)))

	if appendNewLine {
		resolvedContent.Write([]byte(lineBreak))
	}

	return &resolvedContent, nil
}
