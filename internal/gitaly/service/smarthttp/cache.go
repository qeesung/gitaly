package smarthttp

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

type infoRefCache struct {
	logger   log.Logger
	streamer cache.Streamer
}

func newInfoRefCache(logger log.Logger, streamer cache.Streamer) infoRefCache {
	return infoRefCache{
		logger:   logger.WithField("service", uploadPackSvc),
		streamer: streamer,
	}
}

var (
	// prometheus counters
	cacheAttemptTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_inforef_cache_attempt_total",
			Help: "Total number of smarthttp info-ref RPCs accessing the cache",
		},
	)
	hitMissTotals = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_inforef_cache_hit_miss_total",
			Help: "Total number of smarthttp info-ref RPC hit/miss/err cache accesses",
		},
		[]string{"type"},
	)

	// counter functions are package vars to enable easy overriding for tests
	countAttempt = func() { cacheAttemptTotal.Inc() }
	countHit     = func() { hitMissTotals.WithLabelValues("hit").Inc() }
	countMiss    = func() { hitMissTotals.WithLabelValues("miss").Inc() }
	countErr     = func() { hitMissTotals.WithLabelValues("err").Inc() }
)

func (c infoRefCache) tryCache(ctx context.Context, in *gitalypb.InfoRefsRequest, w io.Writer, missFn func(io.Writer) error) error {
	if len(in.GetGitConfigOptions()) > 0 ||
		len(in.GetGitProtocol()) > 0 {
		return missFn(w)
	}

	c.logger.DebugContext(ctx, "Attempting to fetch cached response")
	countAttempt()

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		// The cache uses the requests as the keys. As the request's repository in the RPC handler has been rewritten
		// to point to the transaction's repository, the handler sees each request as different even if they point to
		// the same repository. Restore the original request to ensure identical requests get the same key.
		in = proto.Clone(in).(*gitalypb.InfoRefsRequest)
		in.Repository = tx.OriginalRepository(in.Repository)
	})

	stream, err := c.streamer.GetStream(ctx, in.Repository, in)
	switch {
	case err == nil:
		defer stream.Close()

		countHit()
		c.logger.InfoContext(ctx, "cache hit for UploadPack response")

		if _, err := io.Copy(w, stream); err != nil {
			return structerr.NewInternal("cache copy: %w", err)
		}

		return nil

	case errors.Is(err, cache.ErrReqNotFound):
		countMiss()
		c.logger.InfoContext(ctx, "cache miss for InfoRefsUploadPack response")

		var wg sync.WaitGroup
		defer wg.Wait()

		pr, pw := io.Pipe()

		wg.Add(1)
		go func() {
			defer wg.Done()

			tr := io.TeeReader(pr, w)
			if err := c.streamer.PutStream(ctx, in.Repository, in, tr); err != nil {
				c.logger.WithError(err).ErrorContext(ctx, "unable to store InfoRefsUploadPack response in cache")

				// discard remaining bytes if caching stream
				// failed so that tee reader is not blocked
				_, err = io.Copy(io.Discard, tr)
				if err != nil {
					c.logger.WithError(err).
						ErrorContext(ctx, "unable to discard remaining InfoRefsUploadPack cache stream")
				}
				_ = pr.CloseWithError(err) // always returns nil
			}
		}()

		err = missFn(pw)
		_ = pw.CloseWithError(err) // always returns nil
		return err

	default:
		countErr()
		c.logger.WithError(err).InfoContext(ctx, "unable to fetch cached response")

		return missFn(w)
	}
}
