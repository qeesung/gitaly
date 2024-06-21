package smarthttp

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const concurrentUploadPackThreshold = 5

func (s *server) PostUploadPackWithSidechannel(ctx context.Context, req *gitalypb.PostUploadPackWithSidechannelRequest) (*gitalypb.PostUploadPackWithSidechannelResponse, error) {
	repoPath, gitConfig, err := s.validateUploadPackRequest(ctx, req)
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	var sidechannelRetryableError sidechannel.RetryableError
	conn, err := sidechannel.OpenSidechannel(ctx)
	if err != nil {
		if errors.As(err, &sidechannelRetryableError) {
			// Clients of PostUploadPackWithSidechannel are configured to retry the RPC upon receiving
			// Unavailable, so it should be OK to return it in this case.
			//nolint:gitaly-linters
			return nil, structerr.NewUnavailable("open sidechannel: %w", err)
		}
		return nil, structerr.NewInternal("open sidechannel: %w", err)
	}
	defer conn.Close()

	stats, err := s.runUploadPack(ctx, req, repoPath, gitConfig, conn, conn)
	if err != nil {
		return nil, structerr.NewInternal("running upload-pack: %w", err)
	}

	if err := conn.Close(); err != nil {
		return nil, structerr.NewInternal("close sidechannel connection: %w", err)
	}

	return &gitalypb.PostUploadPackWithSidechannelResponse{
		PackfileNegotiationStatistics: stats.ToProto(),
	}, nil
}

type statsCollector struct {
	c       io.Closer
	statsCh chan stats.PackfileNegotiation
}

func (sc *statsCollector) finish() *stats.PackfileNegotiation {
	sc.c.Close()
	stats := <-sc.statsCh
	return &stats
}

func (s *server) runStatsCollector(ctx context.Context, r io.Reader) (io.Reader, *statsCollector) {
	pr, pw := io.Pipe()
	sc := &statsCollector{
		c:       pw,
		statsCh: make(chan stats.PackfileNegotiation, 1),
	}

	go func() {
		defer close(sc.statsCh)

		stats, err := stats.ParsePackfileNegotiation(pr)
		if err != nil {
			s.logger.WithError(err).DebugContext(ctx, "failed parsing packfile negotiation")
			return
		}
		stats.UpdateMetrics(s.packfileNegotiationMetrics)

		sc.statsCh <- stats
	}()

	return io.TeeReader(r, pw), sc
}

func (s *server) validateUploadPackRequest(ctx context.Context, req *gitalypb.PostUploadPackWithSidechannelRequest) (string, []git.ConfigPair, error) {
	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return "", nil, err
	}
	repoPath, err := s.locator.GetRepoPath(ctx, repository)
	if err != nil {
		return "", nil, err
	}

	git.WarnIfTooManyBitmaps(ctx, s.logger, s.locator, repository.GetStorageName(), repoPath)

	config, err := git.ConvertConfigOptions(req.GetGitConfigOptions())
	if err != nil {
		return "", nil, err
	}

	return repoPath, config, nil
}

func (s *server) runUploadPack(ctx context.Context, req *gitalypb.PostUploadPackWithSidechannelRequest, repoPath string, gitConfig []git.ConfigPair, stdin io.Reader, stdout io.Writer) (stats *stats.PackfileNegotiation, _ error) {
	h := sha1.New()

	stdin = io.TeeReader(stdin, h)
	stdin, collector := s.runStatsCollector(ctx, stdin)
	defer func() {
		if stats == nil {
			stats = collector.finish()
		}
	}()

	gitConfig = append(gitConfig, bundleuri.CapabilitiesGitConfig(ctx)...)

	txID := storage.ExtractTransactionID(ctx)

	var originalRepo *gitalypb.Repository

	if txID != 0 {
		currentTx, err := s.transactionRegistry.Get(txID)
		if err != nil {
			return nil, structerr.NewInternal("error getting transaction: %w", err)
		}
		originalRepo = currentTx.OriginalRepository(req.GetRepository())
	} else {
		originalRepo = req.GetRepository()
	}

	key := originalRepo.GetGlRepository()

	uploadPackConfig, err := bundleuri.UploadPackGitConfig(ctx, s.bundleURISink, req.GetRepository())
	if err != nil {
		if errors.Is(err, bundleuri.ErrBundleNotFound) &&
			featureflag.AutogenerateBundlesForBundleURI.IsEnabled(ctx) &&
			s.generateBundles &&
			s.inflightTracker.GetInProgress(key) > concurrentUploadPackThreshold {

			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
				defer cancel()

				tx, err := s.partitionMgr.Begin(
					ctx,
					originalRepo.GetStorageName(),
					originalRepo.GetRelativePath(),
					0,
					storagemgr.TransactionOptions{
						ReadOnly: true,
					},
				)
				if err != nil {
					ctxlogrus.Extract(ctx).WithError(err).Error("failed starting transaction")
				}

				ctx = storagectx.ContextWithTransaction(ctx, tx)

				if err := s.bundleURISink.GenerateOneAtATime(ctx, localrepo.New(
					s.logger,
					s.locator,
					s.gitCmdFactory,
					s.catfileCache,
					originalRepo)); err != nil {
					ctxlogrus.Extract(ctx).WithError(err).Error("generate bundle")
					if err := tx.Rollback(); err != nil && !errors.Is(err, storagemgr.ErrTransactionAlreadyCommitted) {
						ctxlogrus.Extract(ctx).WithError(err).Error("failed rolling back transaction")
					}
				}

				if err := tx.Commit(ctx); err != nil && !errors.Is(err, storagemgr.ErrTransactionAlreadyCommitted) {
					ctxlogrus.Extract(ctx).WithError(err).Error("committing transaction")
				}
			}()
		} else if !errors.Is(err, bundleuri.ErrSinkMissing) {
			log.AddFields(ctx, log.Fields{"bundle_uri_error": err})
		}
	} else {
		gitConfig = append(gitConfig, uploadPackConfig...)
	}

	var stderr bytes.Buffer

	commandOpts := []git.CmdOpt{
		git.WithStdin(stdin),
		git.WithStderr(&stderr),
		git.WithSetupStdout(),
		git.WithGitProtocol(s.logger, req),
		git.WithConfig(gitConfig...),
		git.WithPackObjectsHookEnv(req.GetRepository(), "http"),
	}

	s.inflightTracker.IncrementInProgress(key)
	defer s.inflightTracker.DecrementInProgress(key)

	cmd, err := s.gitCmdFactory.New(ctx, req.GetRepository(), git.Command{
		Name:  "upload-pack",
		Flags: []git.Option{git.Flag{Name: "--stateless-rpc"}},
		Args:  []string{repoPath},
	}, commandOpts...)
	if err != nil {
		return nil, structerr.NewFailedPrecondition("spawning upload-pack: %w", err)
	}

	// Use a custom buffer size to minimize the number of system calls.
	respBytes, err := io.CopyBuffer(stdout, cmd, make([]byte, 64*1024))
	if err != nil {
		return nil, structerr.NewFailedPrecondition("copying stdout from upload-pack: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		stats = collector.finish()
		if _, ok := command.ExitStatus(err); ok && stats.Deepen != "" {
			// We have seen a 'deepen' message in the request. It is expected that
			// git-upload-pack has a non-zero exit status: don't treat this as an
			// error.
			return stats, nil
		}

		return nil, structerr.NewFailedPrecondition("waiting for upload-pack: %w", err)
	}

	s.logger.WithField("request_sha", fmt.Sprintf("%x", h.Sum(nil))).WithField("response_bytes", respBytes).InfoContext(ctx, "request details")

	return nil, nil
}
