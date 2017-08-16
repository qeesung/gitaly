package git

import (
	"bytes"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/timestamp"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

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

// NewCommit creates a commit based on the given elements
func NewCommit(id, subject, body, authorName, authorEmail, authorDate,
	committerName, committerEmail, committerDate []byte, parentIds ...string) (*pb.GitCommit, error) {
	authorDateTime, err := time.Parse(time.RFC3339, string(authorDate))
	if err != nil {
		return nil, err
	}

	committerDateTime, err := time.Parse(time.RFC3339, string(committerDate))
	if err != nil {
		return nil, err
	}

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
