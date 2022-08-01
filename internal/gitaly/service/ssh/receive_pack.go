package ssh

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) SSHReceivePack(stream gitalypb.SSHService_SSHReceivePackServer) error {
	req, err := stream.Recv() // First request contains only Repository, GlId, and GlUsername
	if err != nil {
		return helper.ErrInternal(err)
	}

	ctxlogrus.Extract(stream.Context()).WithFields(log.Fields{
		"GlID":             req.GlId,
		"GlRepository":     req.GlRepository,
		"GlUsername":       req.GlUsername,
		"GitConfigOptions": req.GitConfigOptions,
		"GitProtocol":      req.GitProtocol,
	}).Debug("SSHReceivePack")

	if err = validateFirstReceivePackRequest(req); err != nil {
		return helper.ErrInvalidArgument(err)
	}

	if err := s.sshReceivePack(stream, req); err != nil {
		return helper.ErrInternal(err)
	}

	return nil
}

func (s *server) sshReceivePack(stream gitalypb.SSHService_SSHReceivePackServer, req *gitalypb.SSHReceivePackRequest) error {
	ctx := stream.Context()

	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})

	var m sync.Mutex
	stdout := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHReceivePackResponse{Stdout: p})
	})

	// We both need to listen in on the stderr stream in order to be able to judge what exactly
	// is happening, but also relay the output to the client. We thus create a MultiWriter to
	// enable both at the same time.
	var stderrBuilder strings.Builder
	stderr := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHReceivePackResponse{Stderr: p})
	})
	stderr = io.MultiWriter(&stderrBuilder, stderr)

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
			Name: "receive-pack",
			Args: []string{repoPath},
		},
		git.WithStdin(stdin),
		git.WithStdout(stdout),
		git.WithStderr(stderr),
		git.WithReceivePackHooks(req, "ssh"),
		git.WithGitProtocol(req),
		git.WithConfig(config...),
	)
	if err != nil {
		return fmt.Errorf("start cmd: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		status, ok := command.ExitStatus(err)
		if !ok {
			return fmt.Errorf("extracting exit status: %w", err)
		}

		// When the command has failed we both want to send its exit status as well as
		// return an error from this RPC call. Otherwise we'd fail the RPC, but return with
		// an `OK` error code to the client.
		if errSend := stream.Send(&gitalypb.SSHReceivePackResponse{
			ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
		}); errSend != nil {
			ctxlogrus.Extract(ctx).WithError(errSend).Error("send final status code")
		}

		// Detect the case where the user has cancelled the push and log it with a proper
		// gRPC error code. We can't do anything about this error anyway and it is a totally
		// valid outcome.
		if stderrBuilder.String() == "fatal: the remote end hung up unexpectedly\n" {
			return helper.ErrCanceledf("user canceled the push")
		}

		return fmt.Errorf("cmd wait: %v", err)
	}

	// In cases where all reference updates are rejected by git-receive-pack(1), we would end up
	// with no transactional votes at all. We thus do a final vote which concludes this RPC to
	// ensure there's always at least one vote. In case there was diverging behaviour in
	// git-receive-pack(1) which led to a different outcome across voters, then this final vote
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

func validateFirstReceivePackRequest(req *gitalypb.SSHReceivePackRequest) error {
	if req.GlId == "" {
		return errors.New("empty GlId")
	}
	if req.Stdin != nil {
		return errors.New("non-empty data in first request")
	}
	if req.Repository == nil {
		return errors.New("repository is empty")
	}

	return nil
}
