package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

const (
	// LooseObjectLimit is the limit of loose objects we accept both when doing incremental
	// repacks and when pruning objects.
	LooseObjectLimit = 1024
)

// ValidateRepacking validates the input repacking config. This function any validating error and if the configuration
// is for full repack.
func ValidateRepacking(cfg config.RepackObjectsConfig) (bool, error) {
	var isFullRepack bool
	switch cfg.Strategy {
	case config.RepackObjectsStrategyIncrementalWithUnreachable:
		isFullRepack = false
		if cfg.WriteBitmap {
			return false, structerr.NewInvalidArgument("cannot write packfile bitmap for an incremental repack")
		}
		if cfg.WriteMultiPackIndex {
			return false, structerr.NewInvalidArgument("cannot write multi-pack index for an incremental repack")
		}
	case config.RepackObjectsStrategyGeometric:
		isFullRepack = false
	case config.RepackObjectsStrategyFullWithCruft, config.RepackObjectsStrategyFullWithUnreachable:
		isFullRepack = true
	default:
		return false, structerr.NewInvalidArgument("invalid strategy: %q", cfg.Strategy)
	}

	if !isFullRepack && !cfg.WriteMultiPackIndex && cfg.WriteBitmap {
		return false, structerr.NewInvalidArgument("cannot write packfile bitmap for an incremental repack")
	}
	if cfg.Strategy != config.RepackObjectsStrategyFullWithCruft && !cfg.CruftExpireBefore.IsZero() {
		return isFullRepack, structerr.NewInvalidArgument("cannot expire cruft objects when not writing cruft packs")
	}

	return isFullRepack, nil
}

// RepackObjects repacks objects in the given repository and updates the commit-graph. The way
// objects are repacked is determined via the config.RepackObjectsConfig.
func RepackObjects(ctx context.Context, repo *localrepo.Repo, cfg config.RepackObjectsConfig) error {
	repoPath, err := repo.Path()
	if err != nil {
		return err
	}
	isFullRepack, err := ValidateRepacking(cfg)
	if err != nil {
		return err
	}

	if isFullRepack {
		// When we have performed a full repack we're updating the "full-repack-timestamp"
		// file. This is done so that we can tell when we have last performed a full repack
		// in a repository. This information can be used by our heuristics to effectively
		// rate-limit the frequency of full repacks.
		//
		// Note that we write the file _before_ actually writing the new pack, which means
		// that even if the full repack fails, we would still pretend to have done it. This
		// is done intentionally, as the likelihood for huge repositories to fail during a
		// full repack is comparatively high. So if we didn't update the timestamp in case
		// of a failure we'd potentially busy-spin trying to do a full repack.
		if err := stats.UpdateFullRepackTimestamp(repoPath, time.Now()); err != nil {
			return fmt.Errorf("updating full-repack timestamp: %w", err)
		}
	}

	switch cfg.Strategy {
	case config.RepackObjectsStrategyIncrementalWithUnreachable:
		if err := PerformIncrementalRepackingWithUnreachable(ctx, repo); err != nil {
			return fmt.Errorf("perform incremental repacking with unreachable: %w", err)
		}

		// The `-d` switch of git-repack(1) handles deletion of objects that have just been
		// packed into a new packfile. As we pack objects ourselves, we have to manually
		// ensure that packed loose objects are deleted.
		var stderr strings.Builder
		if err := repo.ExecAndWait(ctx,
			git.Command{
				Name: "prune-packed",
				Flags: []git.Option{
					// We don't care about any kind of progress meter.
					git.Flag{Name: "--quiet"},
				},
			},
			git.WithStderr(&stderr),
		); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return structerr.New("prune-packed failed with error code %d", exitErr.ExitCode()).WithMetadata("stderr", stderr.String())
			}

			return fmt.Errorf("prune-packed failed: %w", err)
		}

		return nil
	case config.RepackObjectsStrategyFullWithCruft:
		options := []git.Option{
			git.Flag{Name: "--cruft"},
			git.Flag{Name: "--pack-kept-objects"},
			git.Flag{Name: "-l"},
			git.Flag{Name: "-d"},
		}

		if !cfg.CruftExpireBefore.IsZero() {
			options = append(options, git.ValueFlag{
				Name:  "--cruft-expiration",
				Value: git.FormatTime(cfg.CruftExpireBefore),
			})
		}

		return PerformRepack(ctx, repo, cfg, options...)
	case config.RepackObjectsStrategyFullWithUnreachable:
		return PerformFullRepackingWithUnreachable(ctx, repo, cfg)
	case config.RepackObjectsStrategyGeometric:
		return PerformGeometricRepacking(ctx, repo, cfg)
	}
	return nil
}

// PerformIncrementalRepackingWithUnreachable performs an incremental repacking task using git-repack(1) command.
// It packs all loose objects into a pack. This will omit packing objects part of alternates. It does not prune the
// loose objects that were packed.
func PerformIncrementalRepackingWithUnreachable(ctx context.Context, repo *localrepo.Repo) error {
	repoPath, err := repo.Path()
	if err != nil {
		return fmt.Errorf("repo path: %w", err)
	}

	var stderr strings.Builder
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "pack-objects",
			Flags: []git.Option{
				// We ask git-pack-objects(1) to pack loose unreachable
				// objects. This implies `--revs`, but as we don't supply
				// any revisions via stdin all objects will be considered
				// unreachable. The effect is that we simply pack all loose
				// objects into a new packfile, regardless of whether they
				// are reachable or not.
				git.Flag{Name: "--pack-loose-unreachable"},
				// Skip any objects which are part of an alternative object
				// directory.
				git.Flag{Name: "--local"},
				// Only pack objects which are not yet part of a different,
				// local pack.
				git.Flag{Name: "--incremental"},
				// Only create the packfile if it would contain at least one
				// object.
				git.Flag{Name: "--non-empty"},
				// We don't care about any kind of progress meter.
				git.Flag{Name: "--quiet"},
			},
			Args: []string{
				// We need to tell git-pack-objects(1) where to write the
				// new packfile and what prefix it should have. We of course
				// want to write it into the main object directory and have
				// the same "pack-" prefix like normal packfiles would.
				filepath.Join(repoPath, "objects", "pack", "pack"),
			},
		},
		// Note: we explicitly do not pass `GetRepackGitConfig()` here as none of
		// its options apply to this kind of repack: we have no delta islands given
		// that we do not walk the revision graph, and we won't ever write bitmaps.
		git.WithStderr(&stderr),
	); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return structerr.New("pack-objects failed with error code %d", exitErr.ExitCode()).WithMetadata("stderr", stderr.String())
		}

		return fmt.Errorf("pack-objects failed: %w", err)
	}

	return nil
}

// PerformFullRepackingWithUnreachable performs a full repacking task using git-repack(1) command. This will omit packing objects part of alternates.
func PerformFullRepackingWithUnreachable(ctx context.Context, repo *localrepo.Repo, cfg config.RepackObjectsConfig) error {
	return PerformRepack(ctx, repo, cfg,
		// Do a full repack.
		git.Flag{Name: "-a"},
		// Don't include objects part of alternate.
		git.Flag{Name: "-l"},
		// Delete loose objects made redundant by this repack.
		git.Flag{Name: "-d"},
		// Keep unreachable objects part of the old packs in the new pack.
		git.Flag{Name: "--keep-unreachable"},
	)
}

// PerformGeometricRepacking performs geometric repacking task using git-repack(1) command. It allows us to merge
// multiple packfiles without having to rewrite all packfiles into one. This new "geometric" strategy tries to ensure
// that existing packfiles in the repository form a geometric sequence where each successive packfile contains at least
// n times as many objects as the preceding packfile. If the sequence isn't maintained, Git will determine a slice of
// packfiles that it must repack to maintain the sequence again. With this process, we can limit the number of packfiles
// that exist in the repository without having to repack all objects into a single packfile regularly.
// This repacking does not take reachability into account.
// For more information, https://about.gitlab.com/blog/2023/11/02/rearchitecting-git-object-database-mainentance-for-scale/#geometric-repacking
func PerformGeometricRepacking(ctx context.Context, repo *localrepo.Repo, cfg config.RepackObjectsConfig) error {
	return PerformRepack(ctx, repo, cfg,
		// We use a geometric factor `r`, which means that every successively larger
		// packfile must have at least `r` times the number of objects.
		//
		// This factor ultimately determines how many packfiles there can be at a
		// maximum in a repository for a given number of objects. The maximum number
		// of objects with `n` packfiles and a factor `r` is `(1 - r^n) / (1 - r)`.
		// E.g. with a factor of 4 and 10 packfiles, we can have at most 349,525
		// objects, with 16 packfiles we can have 1,431,655,765 objects. Contrary to
		// that, having a factor of 2 will translate to 1023 objects at 10 packfiles
		// and 65535 objects at 16 packfiles at a maximum.
		//
		// So what we're effectively choosing here is how often we need to repack
		// larger parts of the repository. The higher the factor the more we'll have
		// to repack as the packfiles will be larger. On the other hand, having a
		// smaller factor means we'll have to repack less objects as the slices we
		// need to repack will have less objects.
		//
		// The end result is a hybrid approach between incremental repacks and full
		// repacks: we won't typically repack the full repository, but only a subset
		// of packfiles.
		//
		// For now, we choose a geometric factor of two. Large repositories nowadays
		// typically have a few million objects, which would boil down to having at
		// most 32 packfiles in the repository. This number is not scientifically
		// chosen though any may be changed at a later point in time.
		git.ValueFlag{Name: "--geometric", Value: "2"},
		// Make sure to delete loose objects and packfiles that are made obsolete
		// by the new packfile.
		git.Flag{Name: "-d"},
		// Don't include objects part of an alternate.
		git.Flag{Name: "-l"},
	)
}

// PerformRepack performs `git-repack(1)` command on a repository with some pre-built configs.
func PerformRepack(ctx context.Context, repo *localrepo.Repo, cfg config.RepackObjectsConfig, opts ...git.Option) error {
	if cfg.WriteMultiPackIndex {
		opts = append(opts, git.Flag{Name: "--write-midx"})
	}

	var stderr strings.Builder
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "repack",
			Flags: opts,
		},
		git.WithConfig(GetRepackGitConfig(ctx, repo, cfg.WriteBitmap)...),
		git.WithStderr(&stderr),
	); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// We do not embed the `exec.ExitError` directly as it wouldn't typically
			// contain any useful information anyway except for its error code. So we
			// instead only expose what matters and attach stderr to the error metadata.
			return structerr.New("repack failed with error code %d", exitErr.ExitCode()).WithMetadata("stderr", stderr.String())
		}

		return fmt.Errorf("repack failed: %w", err)
	}

	return nil
}

// GetRepackGitConfig returns configuration suitable for Git commands which write new packfiles.
func GetRepackGitConfig(ctx context.Context, repo storage.Repository, bitmap bool) []git.ConfigPair {
	config := []git.ConfigPair{
		{Key: "repack.useDeltaIslands", Value: "true"},
		{Key: "repack.writeBitmaps", Value: strconv.FormatBool(bitmap)},
		{Key: "pack.writeBitmapLookupTable", Value: "true"},
	}

	if storage.IsPoolRepository(repo) {
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

	return config
}

// PruneObjectsConfig determines which objects should be pruned in PruneObjects.
type PruneObjectsConfig struct {
	// ExpireBefore controls the grace period after which unreachable objects shall be pruned.
	// An unreachable object must be older than the given date in order to be considered for
	// deletion.
	ExpireBefore time.Time
}

// PruneObjects prunes loose objects from the repository that are already packed or which are
// unreachable and older than the configured expiry date.
func PruneObjects(ctx context.Context, repo *localrepo.Repo, cfg PruneObjectsConfig) error {
	if err := repo.ExecAndWait(ctx, git.Command{
		Name: "prune",
		Flags: []git.Option{
			// By default, this prunes all unreachable objects regardless of when they
			// have last been accessed. This opens us up for races when there are
			// concurrent commands which are just at the point of writing objects into
			// the repository, but which haven't yet updated any references to make them
			// reachable.
			//
			// To avoid this race, we use a grace window that can be specified by the
			// caller so that we only delete objects that are older than this grace
			// window.
			git.ValueFlag{Name: "--expire", Value: git.FormatTime(cfg.ExpireBefore)},
		},
	}); err != nil {
		return fmt.Errorf("executing prune: %w", err)
	}

	return nil
}
