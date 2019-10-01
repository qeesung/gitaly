package git

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"strings"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
)

func WarnIfTooManyBitmaps(ctx context.Context, repoPath string) {
	if n, err := numBitmaps(repoPath); err == nil && n > 1 {
		grpc_logrus.Extract(ctx).WithField("bitmaps", n).Warn("found more than one packfile bitmap in repository")
	}
}

func numBitmaps(repoPath string) (int, error) {
	packDir := filepath.Join(repoPath, "objects/pack")
	entries, err := ioutil.ReadDir(packDir)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, "pack-") && strings.HasSuffix(name, ".bitmap") {
			count++
		}
	}

	return count, nil
}
