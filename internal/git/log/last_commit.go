package log

import (
	"context"
	"io/ioutil"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// LastCommitForPath returns the last commit which modified path.
func LastCommitForPath(ctx context.Context, batch *catfile.Batch, repo *gitalypb.Repository, revision string, path string) (*gitalypb.GitCommit, error) {
	cmd, err := git.Command(ctx, repo, "log", "--format=%H", "--max-count=1", revision, "--", path)
	if err != nil {
		return nil, err
	}

	commitID, err := ioutil.ReadAll(cmd)
	if err != nil {
		return nil, err
	}

	return GetCommitCatfile(batch, text.ChompBytes(commitID))
}

// GitLogCommand returns a Command that executes git log with the given the arguments
func GitLogCommand(ctx context.Context, repo *gitalypb.Repository, revisions []string, paths []string, extraArgs ...string) (*command.Command, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"Revisions": revisions,
	}).Debug("GitLog")

	formatFlag := "--pretty=%H"

	args := []string{
		"log",
		formatFlag,
	}
	args = append(args, extraArgs...)
	args = append(args, revisions...)
	args = append(args, "--")
	args = append(args, paths...)

	cmd, err := git.Command(ctx, repo, args...)
	if err != nil {
		return nil, err
	}

	return cmd, nil
}
