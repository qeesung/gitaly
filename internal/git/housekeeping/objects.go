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
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

const (
	// looseObjectLimit is the limit of loose objects we accept both when doing incremental
	// repacks and when pruning objects.
	looseObjectLimit = 1024
)

// RepackObjectsStrategy defines how objects shall be repacked.
type RepackObjectsStrategy string

const (
	// RepackObjectsStrategyIncrementalWithUnreachable performs an incremental repack by writing
	// all loose objects into a new packfile, regardless of their reachability. The loose
	// objects will be deleted.
	RepackObjectsStrategyIncrementalWithUnreachable = RepackObjectsStrategy("incremental_with_unreachable")
	// RepackObjectsStrategyFullWithCruft performs a full repack by writing all reachable
	// objects into a new packfile. Unreachable objects will be written into a separate cruft
	// packfile.
	RepackObjectsStrategyFullWithCruft = RepackObjectsStrategy("full_with_cruft")
	// RepackObjectsStrategyFullWithUnreachable performs a full repack by writing all reachable
	// objects into a new packfile. Packed unreachable objects will be appended to the packfile
	// and redundant loose object files will be deleted.
	//
	// Note that this will not include unreachable loose objects, but only packed loose objects.
	// git-repack(1) does not currently expose an option to make it include all objects.
	// Combined with geometric repacks though this is acceptable as the geometric strategy will
	// include all loose objects.
	RepackObjectsStrategyFullWithUnreachable = RepackObjectsStrategy("full_with_unreachable")
	// RepackObjectsStrategyGeometric performs an geometric repack. This strategy will repack
	// packfiles so that the resulting pack structure forms a geometric sequence in the number
	// of objects. Loose objects will get soaked up as part of the repack regardless of their
	// reachability.
	RepackObjectsStrategyGeometric = RepackObjectsStrategy("geometric")
)

// FilterBlobAction defines what we want to do regarding blob offloading.
type FilterBlobAction string

const (
	// FilterBlobActionNoChange means that we should filter blobs only if such filtering is already
	// setup, and we shouldn't change the blob filtering setup.
	FilterBlobActionNoChange = FilterBlobAction("no_change")
	// FilterBlobActionStartFilteringAll means that we should setup filtering all blobs and perform
	// such filtering. If blob filtering is already setup, this should be the same as
	// FilterBlobActionNoChange.
	FilterBlobActionStartFilteringAll = FilterBlobAction("start_filtering_all_blobs")
	// FilterBlobActionStopFilteringAny means that we should remove any setup for filtering all blobs,
	// stop performing such filtering and repack all the blobs with the other Git objects. If blob
	// filtering is not already setup, this should be the same as FilterBlobActionNoChange.
	FilterBlobActionStopFilteringAny = FilterBlobAction("stop_filtering_any_blob")
)

// RepackObjectsConfig is configuration for RepackObjects.
type RepackObjectsConfig struct {
	// Strategy determines the strategy with which to repack objects.
	Strategy RepackObjectsStrategy
	// WriteBitmap determines whether reachability bitmaps should be written or not. There is no
	// reason to set this to `false`, except for legacy compatibility reasons with existing RPC
	// behaviour
	WriteBitmap bool
	// WriteMultiPackIndex determines whether a multi-pack index should be written or not.
	WriteMultiPackIndex bool
	// FilterBlobs determines if we want to start or stop filtering out blobs into a separate
	// packfile, or if we want to continue doing what we already do regarding blob filtering.
	FilterBlobs FilterBlobAction
	// CruftExpireBefore determines the cutoff date before which unreachable cruft objects shall
	// be expired and thus deleted.
	CruftExpireBefore time.Time
}

// RepackObjects repacks objects in the given repository and updates the commit-graph. The way
// objects are repacked is determined via the RepackObjectsConfig.
func RepackObjects(ctx context.Context, repo *localrepo.Repo, cfg RepackObjectsConfig) error {
	repoPath, err := repo.Path()
	if err != nil {
		return err
	}

	var isFullRepack bool
	switch cfg.Strategy {
	case RepackObjectsStrategyIncrementalWithUnreachable, RepackObjectsStrategyGeometric:
		isFullRepack = false
	case RepackObjectsStrategyFullWithCruft, RepackObjectsStrategyFullWithUnreachable:
		isFullRepack = true
	default:
		return structerr.NewInvalidArgument("invalid strategy: %q", cfg.Strategy)
	}

	if !isFullRepack && !cfg.WriteMultiPackIndex && cfg.WriteBitmap {
		return structerr.NewInvalidArgument("cannot write packfile bitmap for an incremental repack")
	}
	if cfg.Strategy != RepackObjectsStrategyFullWithCruft && !cfg.CruftExpireBefore.IsZero() {
		return structerr.NewInvalidArgument("cannot expire cruft objects when not writing cruft packs")
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
	case RepackObjectsStrategyIncrementalWithUnreachable:
		if cfg.WriteBitmap {
			return structerr.NewInvalidArgument("cannot write packfile bitmap for an incremental repack")
		}
		if cfg.WriteMultiPackIndex {
			return structerr.NewInvalidArgument("cannot write multi-pack index for an incremental repack")
		}

		var stderr strings.Builder

		// Pack all loose objects into a new packfile, regardless of their reachability.
		// There is no git-repack(1) mode that would allow us to do this, so we have to
		// instead do it ourselves.
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

		stderr.Reset()

		// The `-d` switch of git-repack(1) handles deletion of objects that have just been
		// packed into a new packfile. As we pack objects ourselves, we have to manually
		// ensure that packed loose objects are deleted.
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
	case RepackObjectsStrategyFullWithCruft:
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

		return performRepack(ctx, repo, cfg, options...)
	case RepackObjectsStrategyFullWithUnreachable:
		return performRepack(ctx, repo, cfg,
			// Do a full repack.
			git.Flag{Name: "-a"},
			// Don't include objects part of alternate.
			git.Flag{Name: "-l"},
			// Delete loose objects made redundant by this repack.
			git.Flag{Name: "-d"},
			// Keep unreachable objects part of the old packs in the new pack.
			git.Flag{Name: "--keep-unreachable"},
		)
	case RepackObjectsStrategyGeometric:
		return performRepack(ctx, repo, cfg,
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
	default:
		return structerr.NewInvalidArgument("invalid strategy: %q", cfg.Strategy)
	}
}

func appendBlobFilterOptions(opts []git.Option, blobOffloadPath string) []git.Option {
	opts = append(opts, git.ValueFlag{Name: "--filter", Value: "blob:none"})
	opts = append(opts, git.ValueFlag{Name: "--filter-to", Value: blobOffloadPath })
	return opts
}

func performRepack(ctx context.Context, repo *localrepo.Repo, cfg RepackObjectsConfig, opts ...git.Option) error {
	if cfg.WriteMultiPackIndex {
		opts = append(opts, git.Flag{Name: "--write-midx"})
	}

	// Blob filtering

	blobOffloadPath := storage.BlobOffloadPath(repo)

	switch cfg.FilterBlobs {
	case FilterBlobActionStartFilteringAll:
		if blobOffloadPath == "" {
			// TODO setup blob filtering, especially the alternates for it.
			// storage.DeriveBlobOffloadPath(repo.ID) should likely be added to
			// repo.GetGitAlternateObjectDirectories()
			// We then need to append the proper options for filtering:
			// opts = appendBlobFilterOptions(opts, storage.DeriveBlobOffloadPath(repo.ID))
		} else {
			// We are already filtering, just add the options
			opts = appendBlobFilterOptions(opts, blobOffloadPath)
		}
	case FilterBlobActionStopFilteringAny:
		if blobOffloadPath != "" {
			// TODO remove blob filtering setup.
			// blobOffloadPath should likely be removed from
			// repo.GetGitAlternateObjectDirectories()
			// Also the blobOffloadPath directory should perhaps be removed from disk.
		}
	case FilterBlobActionNoChange:
		if blobOffloadPath != "" {
			opts = appendBlobFilterOptions(opts, blobOffloadPath)
		}
	default:
		return structerr.NewInvalidArgument("invalid filter blob action: %q", cfg.FilterBlobs)
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
