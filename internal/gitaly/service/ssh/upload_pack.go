package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
)

func (s *server) SSHUploadPack(stream gitalypb.SSHService_SSHUploadPackServer) error {
	ctx := stream.Context()

	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	ctxlogrus.Extract(ctx).WithFields(log.Fields{
		"GlRepository":     req.GetRepository().GetGlRepository(),
		"GitConfigOptions": req.GitConfigOptions,
		"GitProtocol":      req.GitProtocol,
	}).Debug("SSHUploadPack")

	if err = validateFirstUploadPackRequest(req); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	stdin := streamio.NewReader(func() ([]byte, error) {
		request, err := stream.Recv()
		return request.GetStdin(), err
	})

	// gRPC doesn't allow concurrent writes to a stream, so we need to
	// synchronize writing stdout and stderr.
	var m sync.Mutex
	stdout := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadPackResponse{Stdout: p})
	})
	stderr := streamio.NewSyncWriter(&m, func(p []byte) error {
		return stream.Send(&gitalypb.SSHUploadPackResponse{Stderr: p})
	})

	if status, err := s.sshUploadPack(ctx, req, stdin, stdout, stderr); err != nil {
		if errSend := stream.Send(&gitalypb.SSHUploadPackResponse{
			ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
		}); errSend != nil {
			ctxlogrus.Extract(ctx).WithError(errSend).Error("send final status code")
		}

		return structerr.NewInternal("%w", err)
	}

	return nil
}

type sshUploadPackRequest interface {
	GetRepository() *gitalypb.Repository
	GetGitConfigOptions() []string
	GetGitProtocol() string
}

func (s *server) sshUploadPack(rpcContext context.Context, req sshUploadPackRequest, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	ctx, cancelCtx := context.WithCancel(rpcContext)
	defer cancelCtx()

	stdoutCounter := &helper.CountingWriter{W: stdout}
	// Use large copy buffer to reduce the number of system calls
	stdout = &largeBufferReaderFrom{Writer: stdoutCounter}

	repo := req.GetRepository()
	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return 0, err
	}

	git.WarnIfTooManyBitmaps(ctx, s.locator, repo.StorageName, repoPath)

	config, err := git.ConvertConfigOptions(req.GetGitConfigOptions())
	if err != nil {
		return 0, err
	}

	var wg sync.WaitGroup
	pr, pw := io.Pipe()
	defer func() {
		pw.Close()
		wg.Wait()
	}()

	stdin = io.TeeReader(stdin, pw)

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
			pr.Close()
		}()

		stats, err := stats.ParsePackfileNegotiation(pr)
		if err != nil {
			ctxlogrus.Extract(ctx).WithError(err).Debug("failed parsing packfile negotiation")
			return
		}
		stats.UpdateMetrics(s.packfileNegotiationMetrics)
	}()

	commandOpts := []git.CmdOpt{
		git.WithGitProtocol(req),
		git.WithConfig(config...),
		git.WithPackObjectsHookEnv(repo, "ssh"),
	}

	var stderrBuilder strings.Builder
	stderr = io.MultiWriter(stderr, &stderrBuilder)

	cmd, monitor, err := monitorStdinCommand(ctx, s.gitCmdFactory, repo, stdin, stdout, stderr, git.Command{
		Name: "upload-pack",
		Args: []string{repoPath},
	}, commandOpts...)
	if err != nil {
		return 0, err
	}

	timeoutTicker := tick.NewTimerTicker(s.uploadPackRequestTimeout)

	// upload-pack negotiation is terminated by either a flush, or the "done"
	// packet: https://github.com/git/git/blob/v2.20.0/Documentation/technical/pack-protocol.txt#L335
	//
	// "flush" tells the server it can terminate, while "done" tells it to start
	// generating a packfile. Add a timeout to the second case to mitigate
	// use-after-check attacks.
	go monitor.Monitor(ctx, pktline.PktDone(), timeoutTicker, cancelCtx)

	if err := cmd.Wait(); err != nil {
		status, _ := command.ExitStatus(err)

		// When waiting for the packfile negotiation to end times out we'll cancel the local
		// context, but not cancel the overall RPC's context. Our statushandler middleware
		// thus cannot observe the fact that we're cancelling the context, and neither do we
		// provide any valuable information to the caller that we do indeed kill the command
		// because of our own internal timeout.
		//
		// We thus need to special-case the situation where we cancel our own context in
		// order to provide that information and return a proper gRPC error code.
		if ctx.Err() != nil && rpcContext.Err() == nil {
			return status, structerr.NewDeadlineExceeded("waiting for packfile negotiation: %w", ctx.Err())
		}

		// A common error case is that the client is terminating the request prematurely,
		// e.g. by killing their git-fetch(1) process because it's taking too long. This is
		// an expected failure, but we're not in a position to easily tell this error apart
		// from other errors returned by git-upload-pack(1). So we have to resort to parsing
		// the error message returned by Git, and if we see that it matches we return an
		// error with a `Canceled` error code.
		//
		// Note that we're being quite strict with how we match the error for now. We may
		// have to make it more lenient in case we see that this doesn't catch all cases.
		if stderrBuilder.String() == "fatal: the remote end hung up unexpectedly\n" {
			return status, structerr.NewCanceled("user canceled the fetch")
		}

		return status, fmt.Errorf("cmd wait: %w, stderr: %q", err, stderrBuilder.String())
	}

	ctxlogrus.Extract(ctx).WithField("response_bytes", stdoutCounter.N).Info("request details")

	return 0, nil
}

func validateFirstUploadPackRequest(req *gitalypb.SSHUploadPackRequest) error {
	if err := service.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}
	if req.Stdin != nil {
		return errors.New("non-empty stdin in first request")
	}

	return nil
}

type largeBufferReaderFrom struct {
	io.Writer
}

func (rf *largeBufferReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	return io.CopyBuffer(rf.Writer, r, make([]byte, 64*1024))
}

func (s *server) SSHUploadPackWithSidechannel(ctx context.Context, req *gitalypb.SSHUploadPackWithSidechannelRequest) (*gitalypb.SSHUploadPackWithSidechannelResponse, error) {
	conn, err := sidechannel.OpenSidechannel(ctx)
	if err != nil {
		return nil, structerr.NewUnavailable("opennig sidechannel: %w", err)
	}
	defer conn.Close()

	sidebandWriter := pktline.NewSidebandWriter(conn)
	stdout := sidebandWriter.Writer(stream.BandStdout)
	stderr := sidebandWriter.Writer(stream.BandStderr)
	if _, err := s.sshUploadPack(ctx, req, conn, stdout, stderr); err != nil {
		return nil, structerr.NewInternal("%w", err)
	}
	if err := conn.Close(); err != nil {
		return nil, structerr.NewInternal("close sidechannel: %w", err)
	}

	return &gitalypb.SSHUploadPackWithSidechannelResponse{}, nil
}
