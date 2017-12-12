package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/golang/protobuf/ptypes/timestamp"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
)

// FallbackTimeValue is the value returned by `SafeTimeParse` in case it
// encounters a parse error. It's the maximum time value possible in golang.
// See https://gitlab.com/gitlab-org/gitaly/issues/556#note_40289573
var FallbackTimeValue = time.Unix(1<<63-62135596801, 999999999)

// ValidateRevision checks if a revision looks valid
func ValidateRevision(revision []byte) error {
	if len(revision) == 0 {
		return fmt.Errorf("empty revision")
	}
	if bytes.HasPrefix(revision, []byte("-")) {
		return fmt.Errorf("revision can't start with '-'")
	}

	return nil
}

// SafeTimeParse parses a git date string with the RFC3339 format. If the date
// is invalid (possibly because the date is larger than golang's largest value)
// it returns the maximum date possible.
func SafeTimeParse(timeStr string) time.Time {
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return FallbackTimeValue
	}

	return t
}

// NewCommit creates a commit based on the given elements
func NewCommit(id, subject, body, authorName, authorEmail, authorDate,
	committerName, committerEmail, committerDate []byte, parentIds ...string) (*pb.GitCommit, error) {
	authorDateTime := SafeTimeParse(string(authorDate))
	committerDateTime := SafeTimeParse(string(committerDate))

	author := pb.CommitAuthor{
		Name:  authorName,
		Email: authorEmail,
		Date:  &timestamp.Timestamp{Seconds: authorDateTime.Unix()},
	}
	committer := pb.CommitAuthor{
		Name:  committerName,
		Email: committerEmail,
		Date:  &timestamp.Timestamp{Seconds: committerDateTime.Unix()},
	}

	return &pb.GitCommit{
		Id:        string(id),
		Subject:   subject,
		Body:      body,
		Author:    &author,
		Committer: &committer,
		ParentIds: parentIds,
	}, nil
}

// Version returns the used git version.
func Version() (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	cmd, err := command.New(ctx, exec.Command(command.GitPath(), "version"), nil, &buf, nil)
	if err != nil {
		return "", err
	}

	if err = cmd.Wait(); err != nil {
		return "", err
	}

	out := strings.Trim(buf.String(), " \n")
	ver := strings.SplitN(out, " ", 3)
	if len(ver) != 3 {
		return "", fmt.Errorf("invalid version format: %q", buf.String())
	}

	return ver[2], nil
}
