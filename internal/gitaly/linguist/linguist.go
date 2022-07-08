package linguist

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-enry/go-enry/v2"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gitpipe"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
)

// Language is used to parse Linguist's language.json file.
type Language struct {
	Color string `json:"color"`
}

// ByteCountPerLanguage represents a counter value (bytes) per language.
type ByteCountPerLanguage map[string]uint64

// Instance is a holder of the defined in the system language settings.
type Instance struct {
	cfg           config.Cfg
	colorMap      map[string]Language
	gitCmdFactory git.CommandFactory
}

// New loads the name->color map from the Linguist gem and returns initialised instance
// to use back to the caller or an error.
func New(cfg config.Cfg, gitCmdFactory git.CommandFactory) (*Instance, error) {
	jsonReader, err := openLanguagesJSON(cfg)
	if err != nil {
		return nil, err
	}
	defer jsonReader.Close()

	var colorMap map[string]Language
	if err := json.NewDecoder(jsonReader).Decode(&colorMap); err != nil {
		return nil, err
	}

	return &Instance{
		cfg:           cfg,
		gitCmdFactory: gitCmdFactory,
		colorMap:      colorMap,
	}, nil
}

// Stats returns the repository's language stats as reported by 'git-linguist'.
func (inst *Instance) Stats(ctx context.Context, repo *localrepo.Repo, commitID string, catfileCache catfile.Cache) (ByteCountPerLanguage, error) {
	if featureflag.GoLanguageStats.IsEnabled(ctx) {
		return inst.enryStats(ctx, repo, commitID, catfileCache)
	}

	repoPath, err := repo.Path()
	if err != nil {
		return nil, fmt.Errorf("get repo path: %w", err)
	}

	cmd, err := inst.startGitLinguist(ctx, repoPath, commitID)
	if err != nil {
		return nil, fmt.Errorf("starting linguist: %w", err)
	}

	data, err := io.ReadAll(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading linguist output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("waiting for linguist: %w", err)
	}

	stats := make(ByteCountPerLanguage)
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, fmt.Errorf("unmarshaling stats: %w", err)
	}

	return stats, nil
}

// Color returns the color Linguist has assigned to language.
func (inst *Instance) Color(language string) string {
	if color := inst.colorMap[language].Color; color != "" {
		return color
	}

	colorSha := sha256.Sum256([]byte(language))
	return fmt.Sprintf("#%x", colorSha[0:3])
}

func (inst *Instance) startGitLinguist(ctx context.Context, repoPath string, commitID string) (*command.Command, error) {
	bundle, err := exec.LookPath("bundle")
	if err != nil {
		return nil, fmt.Errorf("finding bundle executable: %w", err)
	}

	cmd := exec.Command(bundle, "exec", "bin/gitaly-linguist", "--repository="+repoPath, "--commit="+commitID)
	cmd.Dir = inst.cfg.Ruby.Dir

	internalCmd, err := command.New(ctx, cmd,
		command.WithEnvironment(env.AllowedRubyEnvironment(os.Environ())),
		command.WithCommandName("git-linguist", "stats"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating command: %w", err)
	}

	return internalCmd, nil
}

func openLanguagesJSON(cfg config.Cfg) (io.ReadCloser, error) {
	if jsonPath := cfg.Ruby.LinguistLanguagesPath; jsonPath != "" {
		// This is a fallback for environments where dynamic discovery of the
		// linguist path via Bundler is not working for some reason, for example
		// https://gitlab.com/gitlab-org/gitaly/issues/1119.
		return os.Open(jsonPath)
	}

	linguistPathSymlink, err := os.CreateTemp("", "gitaly-linguist-path")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(linguistPathSymlink.Name()) }()

	if err := linguistPathSymlink.Close(); err != nil {
		return nil, err
	}

	// We use a symlink because we cannot trust Bundler to not print garbage
	// on its stdout.
	rubyScript := `FileUtils.ln_sf(Bundler.rubygems.find_name('github-linguist').first.full_gem_path, ARGV.first)`
	cmd := exec.Command("bundle", "exec", "ruby", "-rfileutils", "-e", rubyScript, linguistPathSymlink.Name())
	cmd.Dir = cfg.Ruby.Dir

	// We have learned that in practice the command we are about to run is a
	// canary for Ruby/Bundler configuration problems. Including stderr and
	// stdout in the gitaly log is useful for debugging such problems.
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%v; stderr: %q", exitError, exitError.Stderr)
		}
		return nil, err
	}

	return os.Open(filepath.Join(linguistPathSymlink.Name(), "lib", "linguist", "languages.json"))
}

func (inst *Instance) enryStats(ctx context.Context, repo *localrepo.Repo, commitID string, catfileCache catfile.Cache) (ByteCountPerLanguage, error) {
	stats, err := newLanguageStats(repo)
	if err != nil {
		ctxlogrus.Extract(ctx).WithError(err).Info("linguist load from cache")
	}
	if stats.CommitID == commitID {
		return stats.Totals, nil
	}

	objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("create object reader: %w", err)
	}
	defer cancel()

	var revlistIt gitpipe.RevisionIterator

	if stats.CommitID == "" {
		// No existing stats cached, so get all the files for the commit
		// using git-ls-tree(1).
		revlistIt = gitpipe.LsTree(ctx, repo,
			commitID,
			gitpipe.LsTreeWithRecursive(),
			gitpipe.LsTreeWithBlobFilter(),
		)
	} else {
		// Stats are cached for one commit, so get the git-diff-tree(1)
		// between that commit and the one we're calculating stats for.

		skipDeleted := func(result *gitpipe.RevisionResult) bool {
			// Skip files that are deleted.
			if result.OID.IsZeroOID() {
				// It's a little bit of a hack to use this skip
				// function, but for every file that's deleted,
				// remove the stats.
				stats.drop(string(result.ObjectName))
				return true
			}
			return false
		}

		revlistIt = gitpipe.DiffTree(ctx, repo,
			stats.CommitID, commitID,
			gitpipe.DiffTreeWithRecursive(),
			gitpipe.DiffTreeWithIgnoreSubmodules(),
			gitpipe.DiffTreeWithSkip(skipDeleted),
		)
	}

	objectIt, err := gitpipe.CatfileObject(ctx, objectReader, revlistIt)
	if err != nil {
		return nil, fmt.Errorf("linguist gitpipe: %w", err)
	}

	for objectIt.Next() {
		object := objectIt.Result()
		filename := string(object.ObjectName)

		// Read arbitrary number of bytes considered enough to determine language
		content, err := io.ReadAll(io.LimitReader(object, 2048))
		if err != nil {
			return nil, fmt.Errorf("linguist read blob: %w", err)
		}

		if _, err := io.Copy(io.Discard, object); err != nil {
			return nil, fmt.Errorf("linguist discard excess blob: %w", err)
		}

		lang := enry.GetLanguage(filename, content)

		// Ignore anything that's neither markup nor a programming language,
		// similar to what the linguist gem does:
		// https://github.com/github/linguist/blob/v7.20.0/lib/linguist/blob_helper.rb#L378-L387
		if enry.GetLanguageType(lang) != enry.Programming &&
			enry.GetLanguageType(lang) != enry.Markup {
			// The file might have been included in the stats before
			stats.drop(filename)

			continue
		}

		stats.add(filename, lang, uint64(object.Object.ObjectSize()))
	}

	if err := objectIt.Err(); err != nil {
		return nil, fmt.Errorf("linguist object iterator: %w", err)
	}

	if err := stats.save(repo, commitID); err != nil {
		return nil, fmt.Errorf("linguist language stats save: %w", err)
	}

	return stats.Totals, nil
}
