package catfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const (
	gpgSignaturePrefix       = "gpgsig"
	gpgSignaturePrefixSha256 = "gpgsig-sha256"
)

// parseCommitState is a type used to define the current state while parsing
// a commit message.
//
// To understand the state macihne of parsing commit messages, we need
// to understand the different sections of a commit message.
//
// # Sections of a commit message
//
// Let's consider a sample commit message
//
//	tree 798e5474fafac9754ee6b82ab17af8d70df4fbd3
//	parent 86f06b3f55e6334abb99fc168e2dd925895c4e49
//	author John doe <bugfixer@email.com> 1699964265 +0100
//	committer John doe <bugfixer@email.com> 1699964265 +0100
//	gpgsig -----BEGIN PGP SIGNATURE-----
//
//	 iHUEABYKAB0WIQReCOKeBZren2AFN0T+9BKLUsDX/wUCZVNlaQAKCRD+9BKLUsDX
//	 /219AP9j8jfQuLieg0Fl8xrOS74eJguYqIsPYI6lPDUvM5XmgQEAkhDUoWFd0ypR
//	 vXTEU/0CxcaXmlco/ThX2rCYwEUT6wA=
//	 =Wt+j
//	 -----END PGP SIGNATURE-----
//
//	Commit subject
//
// With this, we can see that commit messages have
//   - The first section consiting of headers. This is everything before
//     commit message. In our example this consists of the tree, parent,
//     author, committer and gpgsig.
//   - The headers can also contain the signature. This is either gpgsig
//     or gpgsig-256.
//   - After the first line, all lines of the signature start with a ' '
//     space character.
//   - Any headers post the signature are not parsed but will still be
//     considered as part of the signature payload.
//   - Post the headers, there is a newline to differentiate the upcoming
//     commit body. The commit body consists of the commit subject and the
//     message.
//
// Using this information we can now write a parser which represents a state
// machine which can be used to parse commits.
type parseCommitState uint

const (
	parseCommitStateHeader parseCommitState = iota
	parseCommitStateSignature
	parseCommitStateUnexpected
	parseCommitStateBody
	parseCommitStateEnd
)

// SignatureData holds the raw data used to validate a signed commit.
type SignatureData struct {
	// Signatures refers to the signatures present in the commit. Note that
	// Git only considers the first signature when parsing commits
	Signatures [][]byte
	// Payload refers to the commit data which is signed by the signature,
	// generally this is everything apart from the signature in the commit.
	// Headers present after the signature are not considered in the payload.
	Payload []byte
}

// Commit wraps the gitalypb.GitCommit structure and includes signature information.
type Commit struct {
	*gitalypb.GitCommit
	SignatureData SignatureData
}

// ParseCommit implements a state machine to parse the various sections
// of a commit. To understand the state machine, see the definition
// for parseState above.
//
// The goal is to maintain feature parity with how git [1] (see
// parse_buffer_signed_by_header()) itself parses commits. This ensures
// that we throw errors only wherever git does.
//
// [1]: https://gitlab.com/gitlab-org/git/-/blob/master/commit.c
func (p *parser) ParseCommit(object git.Object) (*Commit, error) {
	commit := &gitalypb.GitCommit{Id: object.ObjectID().String()}
	var payload []byte
	currentSignatureIndex := 0
	signatures := [][]byte{}

	bytesRemaining := object.ObjectSize()
	p.bufferedReader.Reset(object)

	for state := parseCommitStateHeader; state != parseCommitStateEnd; {
		receivedEOF := false

		line, err := p.bufferedReader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			receivedEOF = true
		} else if err != nil {
			return nil, fmt.Errorf("parse raw commit: %w", err)
		}
		bytesRemaining -= int64(len(line))

		// If the line only consists of a newline, we can skip
		// the state to commit body.
		if line == "\n" {
			state = parseCommitStateBody
		}

		switch state {
		case parseCommitStateHeader:
			key, value, ok := strings.Cut(line, " ")
			if !ok {
				// TODO: Current tests allow empty commits, we might want
				// to change this behavior.
				goto loopEnd
			}

			// For headers, we trim the newline to make it easier
			// to parse.
			value = strings.TrimSuffix(value, "\n")

			switch key {
			case "parent":
				commit.ParentIds = append(commit.ParentIds, value)
			case "author":
				commit.Author = parseCommitAuthor(value)
			case "committer":
				commit.Committer = parseCommitAuthor(value)
			case "tree":
				commit.TreeId = value
			case "encoding":
				commit.Encoding = value
			case gpgSignaturePrefix, gpgSignaturePrefixSha256:
				// Since Git only considers the first signature, we only
				// capture the first signature's type.
				commit.SignatureType = detectSignatureType(value)

				state = parseCommitStateSignature
				signatures = append(signatures, []byte(value+"\n"))

				goto loopEnd
			}

			payload = append(payload, []byte(line)...)

		case parseCommitStateSignature:
			if after, ok := strings.CutPrefix(line, " "); ok {
				// All signature lines, must start with a ' ' (space).
				signatures[currentSignatureIndex] = append(signatures[currentSignatureIndex], []byte(after)...)
				goto loopEnd
			} else {
				currentSignatureIndex++

				// Multiple signatures might be present in the commit.
				if key, value, ok := strings.Cut(line, " "); ok {
					if key == gpgSignaturePrefix || key == gpgSignaturePrefixSha256 {
						signatures = append(signatures, []byte(value))
						goto loopEnd
					}
				}

				// If there is no ' ' (space), it means there is some unexpected
				// data.
				//
				// Note that we don't go back to parsing headers. This is because
				// any headers which are present after the signature are not parsed
				// by Git as information. But, they still constitute to the signature
				// payload. So any data after the signature and before the commit body
				// is considered unexpected.
				state = parseCommitStateUnexpected
			}

			fallthrough

		case parseCommitStateUnexpected:
			// If the line is only a newline, that means we have reached
			// the commit body. If not, we keep looping till we do.
			if line != "\n" {
				payload = append(payload, []byte(line)...)
				goto loopEnd
			}

			fallthrough

		case parseCommitStateBody:
			payload = append(payload, []byte(line)...)

			body := make([]byte, bytesRemaining)
			if _, err := io.ReadFull(p.bufferedReader, body); err != nil {
				return nil, fmt.Errorf("reading commit message: %w", err)
			}

			// After we have copied the body, we must make sure that there really is no
			// additional data. For once, this is to detect bugs in our implementation where we
			// would accidentally have truncated the commit message. On the other hand, we also
			// need to do this such that we observe the EOF, which we must observe in order to
			// unblock reading the next object.
			//
			// This all feels a bit complicated, where it would be much easier to just read into
			// a preallocated `bytes.Buffer`. But this complexity is indeed required to optimize
			// allocations. So if you want to change this, please make sure to execute the
			// `BenchmarkListAllCommits` benchmark.
			if n, err := io.Copy(io.Discard, p.bufferedReader); err != nil {
				return nil, fmt.Errorf("reading commit message: %w", err)
			} else if n != 0 {
				return nil, fmt.Errorf(
					"commit message exceeds expected length %v by %v bytes",
					object.ObjectSize(), n,
				)
			}

			if len(body) > 0 {
				commit.Subject = subjectFromBody(body)
				commit.BodySize = int64(len(body))
				commit.Body = body
				if max := helper.MaxCommitOrTagMessageSize; len(body) > max {
					commit.Body = commit.Body[:max]
				}
				payload = append(payload, body...)
			}

			state = parseCommitStateEnd
		}

	loopEnd:
		if receivedEOF {
			state = parseCommitStateEnd
		}
	}

	for i, signature := range signatures {
		signatures[i] = bytes.TrimSuffix(signature, []byte("\n"))
	}

	return &Commit{
		GitCommit:     commit,
		SignatureData: SignatureData{Signatures: signatures, Payload: payload},
	}, nil
}
