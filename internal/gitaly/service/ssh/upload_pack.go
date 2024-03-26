package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func (s *server) SSHUploadPack(stream gitalypb.SSHService_SSHUploadPackServer) error {
	ctx := stream.Context()

	req, err := stream.Recv() // First request contains Repository only
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	s.logger.WithFields(log.Fields{
		"GlRepository":     req.GetRepository().GetGlRepository(),
		"GitConfigOptions": req.GitConfigOptions,
		"GitProtocol":      req.GitProtocol,
	}).DebugContext(ctx, "SSHUploadPack")

	if err = validateFirstUploadPackRequest(s.locator, req); err != nil {
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

	if _, status, err := s.sshUploadPack(ctx, req, stdin, stdout, stderr); err != nil {
		if errSend := stream.Send(&gitalypb.SSHUploadPackResponse{
			ExitStatus: &gitalypb.ExitStatus{Value: int32(status)},
		}); errSend != nil {
			s.logger.WithError(errSend).ErrorContext(ctx, "send final status code")
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

func (s *server) sshUploadPack(ctx context.Context, req sshUploadPackRequest, stdin io.Reader, stdout, stderr io.Writer) (negotiation *stats.PackfileNegotiation, _ int, _ error) {
	repo := req.GetRepository()
	repoPath, err := s.locator.GetRepoPath(repo)
	if err != nil {
		return nil, 0, err
	}

	git.WarnIfTooManyBitmaps(ctx, s.logger, s.locator, repo.StorageName, repoPath)

	config, err := git.ConvertConfigOptions(req.GetGitConfigOptions())
	if err != nil {
		return nil, 0, err
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

		stats, errIgnore := stats.ParsePackfileNegotiation(pr)
		negotiation = &stats
		if errIgnore != nil {
			s.logger.WithError(errIgnore).DebugContext(ctx, "failed parsing packfile negotiation")
			return
		}
		stats.UpdateMetrics(s.packfileNegotiationMetrics)
	}()

	config = append(config, bundleuri.CapabilitiesGitConfig(ctx)...)

	uploadPackConfig, err := bundleuri.UploadPackGitConfig(ctx, s.bundleURISink, req.GetRepository())
	if err != nil {
		log.AddFields(ctx, log.Fields{"bundle_uri_error": err})
	} else {
		config = append(config, uploadPackConfig...)
	}

	commandOpts := []git.CmdOpt{
		git.WithGitProtocol(s.logger, req),
		git.WithConfig(config...),
		git.WithPackObjectsHookEnv(repo, "ssh"),
	}

	timeoutTicker := s.uploadPackRequestTimeoutTickerFactory()

	// upload-pack negotiation is terminated by either a flush, or the "done"
	// packet: https://github.com/git/git/blob/v2.20.0/Documentation/technical/pack-protocol.txt#L335
	//
	// "flush" tells the server it can terminate, while "done" tells it to start
	// generating a packfile. Add a timeout to the second case to mitigate
	// use-after-check attacks.
	if err := s.runUploadCommand(ctx, repo, stdin, stdout, stderr, timeoutTicker, pktline.PktDone(), git.Command{
		Name: "upload-pack",
		Args: []string{repoPath},
	}, commandOpts...); err != nil {
		status, _ := command.ExitStatus(err)
		return nil, status, fmt.Errorf("running upload-pack: %w", err)
	}

	return nil, 0, nil
}

func validateFirstUploadPackRequest(locator storage.Locator, req *gitalypb.SSHUploadPackRequest) error {
	if err := locator.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}
	if req.Stdin != nil {
		return errors.New("non-empty stdin in first request")
	}

	return nil
}

func (s *server) SSHUploadPackWithSidechannel(ctx context.Context, req *gitalypb.SSHUploadPackWithSidechannelRequest) (*gitalypb.SSHUploadPackWithSidechannelResponse, error) {
	conn, err := sidechannel.OpenSidechannel(ctx)
	if err != nil {
		return nil, structerr.NewAborted("opennig sidechannel: %w", err)
	}
	defer conn.Close()

	sidebandWriter := pktline.NewSidebandWriter(conn)
	stdout := sidebandWriter.Writer(stream.BandStdout)
	stderr := sidebandWriter.Writer(stream.BandStderr)
	stats, _, err := s.sshUploadPack(ctx, req, conn, stdout, stderr)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}
	if err := conn.Close(); err != nil {
		return nil, structerr.NewInternal("close sidechannel: %w", err)
	}

	return &gitalypb.SSHUploadPackWithSidechannelResponse{
		PackfileNegotiationStatistics: stats.ToProto(),
	}, nil
}
