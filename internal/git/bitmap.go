package git

import (
	"context"
	"os"
	"strings"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/git/packfile"
)

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

	if count == 0 {
		return
	}

	logEntry.WithField("bitmaps", count).Warn("found more than one packfile bitmap in repository")
}
