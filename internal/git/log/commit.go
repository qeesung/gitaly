package log

import (
	"context"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	log "github.com/Sirupsen/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
)

var commitLogFormatFields = []string{
	"%H",  // commit hash
	"%s",  // subject
	"%B",  // raw body (subject + body)
	"%an", // author name
	"%ae", // author email
	"%aI", // author date, strict ISO 8601 format
	"%cn", // committer name
	"%ce", // committer email
	"%cI", // committer date, strict ISO 8601 format
	"%P",  // parent hashes
}

const fieldDelimiterGitFormatString = "%x1f"

// GetCommit returns a single GitCommit
func GetCommit(ctx context.Context, repo *pb.Repository, revision string, path string) (*pb.GitCommit, error) {
	paths := []string{}
	if len(path) > 0 {
		paths = append(paths, path)
	}

	cmd, err := GitLogCommand(ctx, repo, []string{revision}, paths, "--max-count=1")
	if err != nil {
		return nil, err
	}

	logParser := NewLogParser(cmd)
	if ok := logParser.Parse(); !ok {
		return nil, logParser.Err()
	}

	return logParser.Commit(), nil
}

// GitLogCommand returns a Command that executes git log with the given the arguments
func GitLogCommand(ctx context.Context, repo *pb.Repository, revisions []string, paths []string, extraArgs ...string) (*command.Command, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"Revisions": revisions,
	}).Debug("GitLog")

	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return nil, err
	}

	formatFlag := "--pretty=format:" + strings.Join(commitLogFormatFields, fieldDelimiterGitFormatString)

	args := []string{
		"--git-dir",
		repoPath,
		"log",
		"-z", // use 0x00 as the entry terminator (instead of \n)
		formatFlag,
	}
	args = append(args, extraArgs...)
	args = append(args, revisions...)
	args = append(args, "--")
	args = append(args, paths...)

	cmd, err := command.Git(ctx, args...)
	if err != nil {
		return nil, err
	}

	return cmd, nil
}
