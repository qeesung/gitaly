package git

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/packfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/requestinfohandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

var badBitmapRequestCount = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_bad_bitmap_request_total",
		Help: "RPC calls during which there was not exactly 1 packfile bitmap",
	},
	[]string{"method", "bitmaps"},
)

// WarnIfTooManyBitmaps checks for too many (more than one) bitmaps in
// repoPath, and if it finds any, it logs a warning. This is to help us
// investigate https://gitlab.com/gitlab-org/gitaly/issues/1728.
func WarnIfTooManyBitmaps(ctx context.Context, logger log.Logger, locator storage.Locator, storageName, repoPath string) {
	storageRoot, err := locator.GetStorageByName(ctx, storageName)
	if err != nil {
		logger.WithError(err).InfoContext(ctx, "bitmap check failed")
		return
	}

	objdirs, err := ObjectDirectories(ctx, logger, storageRoot, repoPath)
	if err != nil {
		logger.WithError(err).InfoContext(ctx, "bitmap check failed")
		return
	}

	var bitmapCount, packCount int
	seen := make(map[string]bool)
	for _, dir := range objdirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true

		packs, err := packfile.List(dir)
		if err != nil {
			logger.WithError(err).InfoContext(ctx, "bitmap check failed")
			return
		}
		packCount += len(packs)

		for _, p := range packs {
			fi, err := os.Stat(strings.TrimSuffix(p, ".pack") + ".bitmap")
			if err == nil && !fi.IsDir() {
				bitmapCount++
			}
		}
	}

	if bitmapCount == 1 {
		// Exactly one bitmap: this is how things should be.
		return
	}

	if packCount == 0 {
		// If there are no packfiles we don't expect bitmaps nor do we care about
		// them.
		return
	}

	if bitmapCount > 1 {
		logger.WithField("bitmaps", bitmapCount).WarnContext(ctx, "found more than one packfile bitmap in repository")
	}

	// The case where bitmapCount == 0 is likely to occur early in the life of a
	// repository. We don't want to spam our logs with that, so we count but
	// not log it.
	if info := requestinfohandler.Extract(ctx); info != nil {
		badBitmapRequestCount.WithLabelValues(info.FullMethod, strconv.Itoa(bitmapCount)).Inc()
	}
}
