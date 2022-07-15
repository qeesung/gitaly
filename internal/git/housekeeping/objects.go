package housekeeping

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/stats"
)

const (
	// looseObjectLimit is the limit of loose objects we accept both when doing incremental
	// repacks and when pruning objects.
	looseObjectLimit = 1024
)

// RepackObjectsConfig is configuration for RepackObjects.
type RepackObjectsConfig struct {
	// FullRepack determines whether all reachable objects should be repacked into a single
	// packfile. This is much less efficient than doing incremental repacks, which only soak up
	// all loose objects into a new packfile.
	FullRepack bool
	// WriteBitmap determines whether reachability bitmaps should be written or not. There is no
	// reason to set this to `false`, except for legacy compatibility reasons with existing RPC
	// behaviour
	WriteBitmap bool
}

// RepackObjects repacks objects in the given repository and updates the commit-graph. The way
// objects are repacked is determined via the RepackObjectsConfig.
func RepackObjects(ctx context.Context, repo *localrepo.Repo, cfg RepackObjectsConfig) error {
	var options []git.Option
	if cfg.FullRepack {
		options = append(options,
			git.Flag{Name: "-A"},
			git.Flag{Name: "--pack-kept-objects"},
			git.Flag{Name: "-l"},
		)
	}

	if err := repo.ExecAndWait(ctx, git.SubCmd{
		Name: "repack",
		Flags: append([]git.Option{
			git.Flag{Name: "-d"},
			// This can be removed as soon as we have upstreamed a
			// `repack.updateServerInfo` config option. See gitlab-org/git#105 for more
			// details.
			git.Flag{Name: "-n"},
		}, options...),
	}, git.WithConfig(GetRepackGitConfig(ctx, repo, cfg.WriteBitmap)...)); err != nil {
		return err
	}

	stats.LogObjectsInfo(ctx, repo)

	return nil
}

// GetRepackGitConfig returns configuration suitable for Git commands which write new packfiles.
func GetRepackGitConfig(ctx context.Context, repo repository.GitRepo, bitmap bool) []git.ConfigPair {
	config := []git.ConfigPair{
		{Key: "repack.useDeltaIslands", Value: "true"},
	}

	if IsPoolRepository(repo) {
		config = append(config,
			git.ConfigPair{Key: "pack.island", Value: git.ObjectPoolRefNamespace + "/he(a)ds"},
			git.ConfigPair{Key: "pack.island", Value: git.ObjectPoolRefNamespace + "/t(a)gs"},
			git.ConfigPair{Key: "pack.islandCore", Value: "a"},
		)
	} else {
		config = append(config,
			git.ConfigPair{Key: "pack.island", Value: "r(e)fs/heads"},
			git.ConfigPair{Key: "pack.island", Value: "r(e)fs/tags"},
			git.ConfigPair{Key: "pack.islandCore", Value: "e"},
		)
	}

	if bitmap {
		config = append(config, git.ConfigPair{Key: "repack.writeBitmaps", Value: "true"})
		config = append(config, git.ConfigPair{Key: "pack.writeBitmapHashCache", Value: "true"})
	} else {
		config = append(config, git.ConfigPair{Key: "repack.writeBitmaps", Value: "false"})
	}

	return config
}
