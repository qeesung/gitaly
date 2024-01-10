package catfile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Parser parses Git objects into their gitalypb representations.
type Parser interface {
	ParseCommit(object git.Object) (*Commit, error)
	ParseTag(object git.Object) (*gitalypb.Tag, error)
}

type parser struct {
	bufferedReader *bufio.Reader
}

// NewParser creates a new parser for Git objects.
func NewParser() Parser {
	return newParser()
}

func newParser() *parser {
	return &parser{
		bufferedReader: bufio.NewReader(nil),
	}
}

const maxUnixCommitDate = 1 << 53

// fallbackTimeValue is the value returned in case there is a parse error. It's the maximum
// time value possible in golang. See
// https://gitlab.com/gitlab-org/gitaly/issues/556#note_40289573
var fallbackTimeValue = time.Unix(1<<63-62135596801, 999999999)

func parseCommitAuthor(line string) *gitalypb.CommitAuthor {
	author := &gitalypb.CommitAuthor{}

	name, line, ok := strings.Cut(line, "<")
	author.Name = []byte(strings.TrimSuffix(name, " "))

	if !ok {
		return author
	}

	email, line, ok := strings.Cut(line, ">")
	if !ok {
		return author
	}

	author.Email = []byte(email)

	secSplit := strings.Fields(line)
	if len(secSplit) < 1 {
		return author
	}

	sec, err := strconv.ParseInt(secSplit[0], 10, 64)
	if err != nil || sec > maxUnixCommitDate || sec < 0 {
		sec = fallbackTimeValue.Unix()
	}

	author.Date = &timestamppb.Timestamp{Seconds: sec}

	if len(secSplit) == 2 {
		author.Timezone = []byte(secSplit[1])
	}

	return author
}

func subjectFromBody(body []byte) []byte {
	subject, _, _ := bytes.Cut(body, []byte("\n"))
	return bytes.TrimRight(subject, "\r\n")
}

func detectSignatureType(line string) gitalypb.SignatureType {
	switch strings.TrimSuffix(line, "\n") {
	case "-----BEGIN SIGNED MESSAGE-----":
		return gitalypb.SignatureType_X509
	case "-----BEGIN PGP MESSAGE-----":
		return gitalypb.SignatureType_PGP
	case "-----BEGIN PGP SIGNATURE-----":
		return gitalypb.SignatureType_PGP
	case "-----BEGIN SSH SIGNATURE-----":
		return gitalypb.SignatureType_SSH
	default:
		return gitalypb.SignatureType_NONE
	}
}

// ParseTag parses the given object, which is expected to refer to a Git tag. The tag's tagged
// commit is not populated. The given object ID shall refer to the tag itself such that the returned
// Tag structure has the correct OID.
func (p *parser) ParseTag(object git.Object) (*gitalypb.Tag, error) {
	tag, _, err := p.parseTag(object, nil)
	return tag, err
}

type taggedObject struct {
	objectID   string
	objectType string
}

func (p *parser) parseTag(object git.Object, name []byte) (*gitalypb.Tag, taggedObject, error) {
	p.bufferedReader.Reset(object)

	tag := &gitalypb.Tag{
		Id:   object.ObjectID().String(),
		Name: name,
	}
	var tagged taggedObject
	var lastLine bool
	bytesRemaining := object.ObjectSize()

	for !lastLine {
		line, err := p.bufferedReader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			lastLine = true
		} else if err != nil {
			return nil, taggedObject{}, fmt.Errorf("reading tag header: %w", err)
		}
		bytesRemaining -= int64(len(line))

		if len(line) == 0 {
			continue
		}
		if line == "\n" {
			break
		}

		if line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}

		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		switch key {
		case "object":
			tagged.objectID = value
		case "type":
			tagged.objectType = value
		case "tag":
			if len(tag.Name) == 0 {
				tag.Name = []byte(value)
			}
		case "tagger":
			tag.Tagger = parseCommitAuthor(value)
		}
	}

	// We only need to try reading the message if we haven't seen an EOF yet, but instead saw
	// the "\n\n" separator.
	if !lastLine {
		message := make([]byte, bytesRemaining)
		if _, err := io.ReadFull(p.bufferedReader, message); err != nil {
			return nil, taggedObject{}, fmt.Errorf("reading tag message: %w", err)
		}

		// After we have copied the message, we must make sure that there really is no
		// additional data. For once, this is to detect bugs in our implementation where we
		// would accidentally have truncated the commit message. On the other hand, we also
		// need to do this such that we observe the EOF, which we must observe in order to
		// unblock reading the next object.
		//
		// This all feels a bit complicated, where it would be much easier to just read into
		// a preallocated `bytes.Buffer`. But this complexity is indeed required to optimize
		// allocations. So if you want to change this, please make sure to execute the
		// `BenchmarkFindAllTags` benchmark.
		if n, err := io.Copy(io.Discard, p.bufferedReader); err != nil {
			return nil, taggedObject{}, fmt.Errorf("reading tag message: %w", err)
		} else if n != 0 {
			return nil, taggedObject{}, fmt.Errorf(
				"tag message exceeds expected length %v by %v bytes",
				object.ObjectSize(), n,
			)
		}

		tag.Message = message
		tag.MessageSize = int64(len(message))

		if signature, _ := ExtractTagSignature(message); signature != nil {
			length := bytes.Index(signature, []byte("\n"))

			if length > 0 {
				signature := string(signature[:length])
				tag.SignatureType = detectSignatureType(signature)
			}
		}
	}

	return tag, tagged, nil
}
