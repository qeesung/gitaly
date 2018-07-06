package commit

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/internal/git/log"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
)

type commitsSender interface {
	Send([]*pb.GitCommit) error
}

func sendCommits(ctx context.Context, sender commitsSender, repo *pb.Repository, revisionRange []string, paths []string, extraArgs ...string) error {
	cmd, err := log.GitLogCommand(ctx, repo, revisionRange, paths, extraArgs...)
	if err != nil {
		return err
	}

	logParser, err := log.NewLogParser(ctx, repo, cmd)
	if err != nil {
		return err
	}

	var commits []*pb.GitCommit
	commitsSize := 0

	for logParser.Parse() {
		commit := logParser.Commit()
		commitsSize += commitSize(commit)

		if commitsSize >= maxMsgSize {
			if err := sender.Send(commits); err != nil {
				return err
			}
			commits = nil
			commitsSize = 0
		}

		commits = append(commits, commit)
	}

	if err := logParser.Err(); err != nil {
		return err
	}

	if err := sender.Send(commits); err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		// We expect this error to be caused by non-existing references. In that
		// case, we just log the error and send no commits to the `sender`.
		grpc_logrus.Extract(ctx).WithError(err).Info("ignoring git-log error")
	}

	return nil
}

func commitSize(commit *pb.GitCommit) int {
	size := len(commit.Id) + len(commit.Subject) + len(commit.Body) + len(commit.ParentIds)*40

	author := commit.GetAuthor()
	size += len(author.GetName()) + len(author.GetEmail())

	committer := commit.GetCommitter()
	size += len(committer.GetName()) + len(committer.GetEmail())

	size += 8 + 8 // Author and Committer timestamps are int64

	return size
}
