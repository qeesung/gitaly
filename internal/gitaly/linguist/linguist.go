package linguist

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/go-enry/go-enry/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gitattributes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// ByteCountPerLanguage represents a counter value (bytes) per language.
type ByteCountPerLanguage map[string]uint64

// Instance is a holder of the defined in the system language settings.
type Instance struct {
	cfg          config.Cfg
	logger       log.Logger
	catfileCache catfile.Cache
	repo         *localrepo.Repo
}

// New creates a new instance that can be used to calculate language stats for
// the given repo.
func New(cfg config.Cfg, logger log.Logger, catfileCache catfile.Cache, repo *localrepo.Repo) *Instance {
	return &Instance{
		cfg:          cfg,
		logger:       logger,
		catfileCache: catfileCache,
		repo:         repo,
	}
}

// Color returns the color Linguist has assigned to language.
func Color(language string) string {
	if color := enry.GetColor(language); color != "#cccccc" {
		return color
	}

	colorSha := sha256.Sum256([]byte(language))
	return fmt.Sprintf("#%x", colorSha[0:3])
}

// Stats returns the repository's language statistics.
func (inst *Instance) Stats(ctx context.Context, commitID git.ObjectID) (ByteCountPerLanguage, error) {
	stats, err := initLanguageStats(ctx, inst.repo)
	if err != nil {
		inst.logger.WithError(err).InfoContext(ctx, "linguist load from cache")
	}
	if stats.CommitID == commitID {
		return stats.Totals, nil
	}

	objectReader, cancel, err := inst.catfileCache.ObjectReader(ctx, inst.repo)
	if err != nil {
		return nil, fmt.Errorf("linguist create object reader: %w", err)
	}
	defer cancel()

	checkAttr, finishAttr, err := gitattributes.CheckAttr(ctx, inst.repo, commitID.Revision(), linguistAttrs)
	if err != nil {
		return nil, fmt.Errorf("linguist create check attr: %w", err)
	}
	defer finishAttr()

	var revlistIt gitpipe.RevisionIterator

	full, err := inst.needsFullRecalculation(ctx, stats.CommitID, commitID)
	if err != nil {
		return nil, fmt.Errorf("linguist cannot determine full recalculation: %w", err)
	}

	if full {
		stats = newLanguageStats()

		skipFunc := func(result *gitpipe.RevisionResult) (bool, error) {
			f, err := newFileInstance(string(result.ObjectName), checkAttr)
			if err != nil {
				return true, fmt.Errorf("new file instance: %w", err)
			}

			// Skip files that are an excluded filetype based on filename.
			return f.IsExcluded(), nil
		}

		// Full recalculation is needed, so get all the files for the
		// commit using git-ls-tree(1).
		revlistIt = gitpipe.LsTree(ctx, inst.repo,
			commitID.String(),
			gitpipe.LsTreeWithRecursive(),
			gitpipe.LsTreeWithBlobFilter(),
			gitpipe.LsTreeWithSkip(skipFunc),
		)
	} else {
		// Stats are cached for one commit, so get the git-diff-tree(1)
		// between that commit and the one we're calculating stats for.

		hash, err := inst.repo.ObjectHash(ctx)
		if err != nil {
			return nil, fmt.Errorf("linguist: detect object hash: %w", err)
		}

		skipFunc := func(result *gitpipe.RevisionResult) (bool, error) {
			var skip bool

			// Skip files that are deleted, or
			// an excluded filetype based on filename.
			if hash.IsZeroOID(result.OID) {
				skip = true
			} else {
				f, err := newFileInstance(string(result.ObjectName), checkAttr)
				if err != nil {
					return false, fmt.Errorf("new file instance: %w", err)
				}
				skip = f.IsExcluded()
			}

			if skip {
				// It's a little bit of a hack to use this skip
				// function, but for every file that's deleted,
				// remove the stats.
				stats.drop(string(result.ObjectName))
				return true, nil
			}
			return false, nil
		}

		revlistIt = gitpipe.DiffTree(ctx, inst.repo,
			stats.CommitID.String(), commitID.String(),
			gitpipe.DiffTreeWithRecursive(),
			gitpipe.DiffTreeWithIgnoreSubmodules(),
			gitpipe.DiffTreeWithSkip(skipFunc),
		)
	}

	objectIt, err := gitpipe.CatfileObject(ctx, objectReader, revlistIt)
	if err != nil {
		return nil, fmt.Errorf("linguist gitpipe: %w", err)
	}

	for objectIt.Next() {
		object := objectIt.Result()
		filename := string(object.ObjectName)

		f, err := newFileInstance(filename, checkAttr)
		if err != nil {
			return nil, fmt.Errorf("linguist new file instance: %w", err)
		}

		lang, size, err := f.DetermineStats(object)
		if err != nil {
			return nil, fmt.Errorf("linguist determine stats: %w", err)
		}

		// Ensure object content is completely consumed
		if _, err := io.Copy(io.Discard, object); err != nil {
			return nil, fmt.Errorf("linguist discard excess blob: %w", err)
		}

		if len(lang) == 0 {
			stats.drop(filename)

			continue
		}

		stats.add(filename, lang, size)
	}

	if err := objectIt.Err(); err != nil {
		return nil, fmt.Errorf("linguist object iterator: %w", err)
	}

	if err := stats.save(ctx, inst.repo, commitID); err != nil {
		return nil, fmt.Errorf("linguist language stats save: %w", err)
	}

	return stats.Totals, nil
}

func (inst *Instance) needsFullRecalculation(ctx context.Context, cachedID, commitID git.ObjectID) (bool, error) {
	if cachedID == "" {
		return true, nil
	}

	err := inst.repo.ExecAndWait(ctx, git.Command{
		Name:        "diff",
		Flags:       []git.Option{git.Flag{Name: "--quiet"}},
		Args:        []string{fmt.Sprintf("%v..%v", cachedID, commitID)},
		PostSepArgs: []string{".gitattributes"},
	})
	if err == nil {
		return false, nil
	}
	if code, ok := command.ExitStatus(err); ok && code == 1 {
		return true, nil
	}

	return true, fmt.Errorf("git diff .gitattributes: %w", err)
}
