package git

import (
	"context"
	"os"
	"strconv"
	"strings"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/git/packfile"
)

var badBitmapRequestCount = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_bad_bitmap_request_total",
		Help: "RPC calls during which there was not exactly 1 packfile bitmap",
	},
	[]string{"method", "bitmaps"},
)

func init() { prometheus.MustRegister(badBitmapRequestCount) }

// WarnIfTooManyBitmaps checks for too many (more than one) bitmaps in
// repoPath, and if it finds any, it logs a warning. This is to help us
// investigate https://gitlab.com/gitlab-org/gitaly/issues/1728.
func WarnIfTooManyBitmaps(ctx context.Context, repoPath string) {
	logEntry := grpc_logrus.Extract(ctx)

	objdirs, err := ObjectDirectories(repoPath)
	if err != nil {
		logEntry.WithError(err).Info("bitmap check failed")
		return
	}

	var count int
	seen := make(map[string]struct{})
	for _, dir := range objdirs {
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}

		packs, err := packfile.List(dir)
		if err != nil {
			logEntry.WithError(err).Info("bitmap check failed")
			return
		}

		for _, p := range packs {
			fi, err := os.Stat(strings.TrimSuffix(p, ".pack") + ".bitmap")
			if err == nil && !fi.IsDir() {
				count++
			}
		}
	}

	if count == 1 {
		return
	}

	if count > 1 {
		logEntry.WithField("bitmaps", count).Warn("found more than one packfile bitmap in repository")
	}

	grpcMethod, ok := grpc_ctxtags.Extract(ctx).Values()["grpc.request.fullMethod"].(string)
	if !ok {
		return
	}
	badBitmapRequestCount.WithLabelValues(grpcMethod, strconv.Itoa(count)).Inc()
}
