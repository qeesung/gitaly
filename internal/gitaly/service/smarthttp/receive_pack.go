package smarthttp

import (
	"errors"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) PostReceivePack(stream gitalypb.SmartHTTPService_PostReceivePackServer) error {
	ctx := stream.Context()
	req, err := stream.Recv() // First request contains only Repository and GlId
	if err != nil {
		return err
	}

	ctxlogrus.Extract(ctx).WithFields(log.Fields{
		"GlID":             req.GlId,
		"GlRepository":     req.GlRepository,
		"GlUsername":       req.GlUsername,
		"GitConfigOptions": req.GitConfigOptions,
	}).Debug("PostReceivePack")

	if err := validateReceivePackRequest(req); err != nil {
		return err
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
	stdout := streamio.NewWriter(func(p []byte) error {
		return stream.Send(&gitalypb.PostReceivePackResponse{Data: p})
	})

	repoPath, err := s.locator.GetRepoPath(req.Repository)
	if err != nil {
		return err
	}

	config, err := git.ConvertConfigOptions(req.GitConfigOptions)
	if err != nil {
		return err
	}

	cmd, err := s.gitCmdFactory.NewWithoutRepo(ctx,
		git.SubCmd{
			Name:  "receive-pack",
			Flags: []git.Option{git.Flag{Name: "--stateless-rpc"}},
			Args:  []string{repoPath},
		},
		git.WithStdin(stdin),
		git.WithStdout(stdout),
		git.WithReceivePackHooks(req, "http"),
		git.WithGitProtocol(req),
		git.WithConfig(config...),
	)
	if err != nil {
		return status.Errorf(codes.Unavailable, "PostReceivePack: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return status.Errorf(codes.Unavailable, "PostReceivePack: %v", err)
	}

	// In cases where all reference updates are rejected by git-receive-pack(1), we would end up
	// with no transactional votes at all. This would lead to scheduling
	// replication jobs, which wouldn't accomplish anything since no refs
	// were updated.
	// To prevent replication jobs from being unnecessarily created, do a
	// final vote which concludes this RPC to ensure there's always at least
	// one vote. In case there was diverging behaviour in git-receive-pack(1)
	// which led to a different outcome across voters, then this final vote
	// would fail because the sequence of votes would be different.
	if err := transaction.VoteOnContext(ctx, s.txManager, voting.Vote{}, voting.Committed); err != nil {
		// When the pre-receive hook failed, git-receive-pack(1) exits with code 0.
		// It's arguable whether this is the expected behavior, but anyhow it means
		// cmd.Wait() did not error out. On the other hand, the gitaly-hooks command did
		// stop the transaction upon failure. So this final vote fails.
		// To avoid this error being presented to the end user, ignore it when the
		// transaction was stopped.
		if !errors.Is(err, transaction.ErrTransactionStopped) {
			return status.Errorf(codes.Aborted, "final transactional vote: %v", err)
		}
	}

	return nil
}

func validateReceivePackRequest(req *gitalypb.PostReceivePackRequest) error {
	if req.GlId == "" {
		return status.Errorf(codes.InvalidArgument, "PostReceivePack: empty GlId")
	}
	if req.Data != nil {
		return status.Errorf(codes.InvalidArgument, "PostReceivePack: non-empty Data")
	}
	if req.Repository == nil {
		return helper.ErrInvalidArgumentf("PostReceivePack: empty Repository")
	}

	return nil
}
