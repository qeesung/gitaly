package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/adapters/gogit"
	"gitlab.com/gitlab-org/gitaly/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/log"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	findCommitRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_find_commit_requests_total",
			Help: "Counter of FindCommit requests, separated by backend",
		},
		[]string{"backend", "status"},
	)
)

func init() {
	prometheus.MustRegister(findCommitRequests)
}

func (s *server) FindCommit(ctx context.Context, in *pb.FindCommitRequest) (*pb.FindCommitResponse, error) {
	revision := in.GetRevision()
	if err := git.ValidateRevision(revision); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "FindCommit: revision: %v", err)
	}

	repo := in.GetRepository()

	if featureflag.IsEnabled(ctx, "gogit-findcommit") {
		commit, err := gogitFindCommit(repo, revision)
		if err == nil {
			findCommitRequests.WithLabelValues("go-git", "OK").Inc()
			return &pb.FindCommitResponse{Commit: commit}, nil
		}
		findCommitRequests.WithLabelValues("go-git", "Fail").Inc()
	}

	commit, err := shelloutFindCommit(ctx, repo, revision)
	if err == nil {
		findCommitRequests.WithLabelValues("spawn-git", "OK").Inc()
	} else {
		findCommitRequests.WithLabelValues("spawn-git", "Fail").Inc()
	}

	return &pb.FindCommitResponse{Commit: commit}, err
}

func gogitFindCommit(repo *pb.Repository, revision []byte) (*pb.GitCommit, error) {
	repoPath, _, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "FindCommit: repository path not found")
	}

	var commit *pb.GitCommit
	commit, err = gogit.FindCommit(repoPath, string(revision))
	if err != nil {
		return nil, err
	}

	return commit, nil
}

func shelloutFindCommit(ctx context.Context, repo *pb.Repository, revision []byte) (*pb.GitCommit, error) {
	commit, err := log.GetCommit(ctx, repo, string(revision), "")
	if err != nil {
		return nil, err
	}

	return commit, err
}
