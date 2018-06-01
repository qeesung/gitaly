package gogit

import (
	"strings"

	"github.com/golang/protobuf/ptypes/timestamp"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

// FindCommit resolves the revision and returns the commit found
func FindCommit(repoPath, revision string) (*pb.GitCommit, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, status.Error(codes.Internal, "FindCommit: unabled to open the repository")
	}

	h, err := repo.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return nil, err
	}

	commit, err := object.GetCommit(repo.Storer, *h)
	if err != nil {
		return nil, err
	}

	pbCommit := commitToProtoCommit(commit)

	return pbCommit, nil
}

func commitToProtoCommit(commit *object.Commit) *pb.GitCommit {
	// Ripped from internal/git/log/commitmessage.go getCommitMessage()
	// It seems to send the same bits twice?
	var subject string
	if split := strings.SplitN(commit.Message, "\n", 2); len(split) == 2 {
		subject = strings.TrimRight(split[0], "\r\n")
	}
	body := commit.Message

	parentIds := make([]string, len(commit.ParentHashes))
	for i, hash := range commit.ParentHashes {
		parentIds[i] = hash.String()
	}

	author := pb.CommitAuthor{
		Name:  []byte(commit.Author.Name),
		Email: []byte(commit.Author.Email),
		Date:  &timestamp.Timestamp{Seconds: commit.Author.When.Unix()},
	}
	committer := pb.CommitAuthor{
		Name:  []byte(commit.Committer.Name),
		Email: []byte(commit.Committer.Email),
		Date:  &timestamp.Timestamp{Seconds: commit.Committer.When.Unix()},
	}

	byteBody := []byte(body)
	newCommit := &pb.GitCommit{
		Id:        commit.ID().String(),
		Subject:   []byte(subject),
		Body:      byteBody,
		BodySize:  int64(len(byteBody)),
		Author:    &author,
		Committer: &committer,
		ParentIds: parentIds,
	}

	if threshold := helper.MaxCommitOrTagMessageSize; int(newCommit.BodySize) > threshold {
		newCommit.Body = newCommit.Body[:threshold]
	}

	return newCommit
}
