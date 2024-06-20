package ssh

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/receivepack"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) SSHReceivePack(stream gitalypb.SSHService_SSHReceivePackServer) error {
	req, err := stream.Recv() // First request contains only Repository, GlId, and GlUsername
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	s.logger.WithFields(log.Fields{
		"GlID":             req.GlId,
		"GlRepository":     req.GlRepository,
		"GlUsername":       req.GlUsername,
		"GitConfigOptions": req.GitConfigOptions,
		"GitProtocol":      req.GitProtocol,
	}).DebugContext(stream.Context(), "SSHReceivePack")

	if err = validateFirstReceivePackRequest(s.locator, req); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if err := s.sshReceivePack(stream, req); err != nil {
		return structerr.NewInternal("%w", err)
	}

	// In cases where all reference updates are rejected by git-receive-pack(1), we would end up
	// with no transactional votes at all. We thus do a final vote which concludes this RPC to
	// ensure there's always at least one vote. In case there was diverging behaviour in
	// git-receive-pack(1) which led to a different outcome across voters, then this final vote
	// would fail because the sequence of votes would be different.
	if err := transaction.VoteOnContext(stream.Context(), s.txManager, voting.Vote{}, voting.Committed); err != nil {
		// When the pre-receive hook failed, git-receive-pack(1) exits with code 0.
		// It's arguable whether this is the expected behavior, but anyhow it means
		// cmd.Wait() did not error out. On the other hand, the gitaly-hooks command did
		// stop the transaction upon failure. So this final vote fails.
		// To avoid this error being presented to the end user, ignore it when the
		// transaction was stopped.
		if !errors.Is(err, transaction.ErrTransactionStopped) {
			return structerr.NewAborted("final transactional vote: %w", err)
		}
	}

	return nil
}

func (s *server) sshReceivePack(stream gitalypb.SSHService_SSHReceivePackServer, req *gitalypb.SSHReceivePackRequest) (returnedErr error) {
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

	// When an `exec.Cmd` has its `cmd.Stdin` configured with an `io.Reader`
	// that is not also of type `os.File` a goroutine is automatically
	// configured that performs an `io.Copy()` between the reader and a newly
	// created pipe. A problem with this can arise when `cmd.Wait()` is invoked
	// because it waits not only for the process to complete but also all the
	// goroutine to end. If the configured `cmd.Stdin` is only of type
	// `io.Reader` and never closed, the goroutine will never end. This leads to
	// `cmd.Wait()` being blocked indefinitely.
	//
	// Within Gitaly this problem can manifest itself when a git process crashes
	// before `stdin` reaches EOF. To date this has only been noticed as a
	// problem for the `SSHReceivePack` RPC, so a pipe and goroutine have been
	// created explicitly to prevent `cmd.Wait()` from blocking indefinitely.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating pipe: %w", err)
	}

	go func() {
		_, _ = io.Copy(pw, stdin)
		_ = pw.Close()
	}()

	transactionID := storage.ExtractTransactionID(ctx)
	transactionsEnabled := transactionID > 0
	if transactionsEnabled {
		repo := s.localrepo(req.GetRepository())
		procReceiveCleanup, err := receivepack.RegisterProcReceiveHook(
			ctx,
			s.logger,
			s.cfg,
			req,
			repo,
			s.hookManager,
			hook.NewTransactionRegistry(s.txRegistry),
			transactionID,
			stdout,
			stderr,
		)
		if err != nil {
			return err
		}
		defer func() {
			if err := procReceiveCleanup(); err != nil && returnedErr == nil {
				returnedErr = err
			}
		}()
	}

	cmd, err := s.gitCmdFactory.New(ctx, req.GetRepository(),
		git.Command{
			Name: "receive-pack",
			Args: []string{repoPath},
		},
		git.WithStdin(pr),
		git.WithStdout(stdout),
		git.WithStderr(stderr),
		git.WithReceivePackHooks(req, "ssh", transactionsEnabled),
		git.WithGitProtocol(s.logger, req),
		git.WithConfig(config...),
	)
	if err != nil {
		return fmt.Errorf("start cmd: %w", err)
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
			s.logger.WithError(errSend).ErrorContext(ctx, "send final status code")
		}

		// Detect the case where the user has cancelled the push and log it with a proper
		// gRPC error code. We can't do anything about this error anyway and it is a totally
		// valid outcome.
		if stderrBuilder.String() == "fatal: the remote end hung up unexpectedly\n" {
			return structerr.NewCanceled("user canceled the push")
		}

		return fmt.Errorf("cmd wait: %w", err)
	}

	return nil
}

func validateFirstReceivePackRequest(locator storage.Locator, req *gitalypb.SSHReceivePackRequest) error {
	if req.GlId == "" {
		return errors.New("empty GlId")
	}
	if req.Stdin != nil {
		return errors.New("non-empty data in first request")
	}
	return locator.ValidateRepository(req.GetRepository())
}
