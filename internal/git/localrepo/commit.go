package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

var (
	// ErrMissingTree indicates a missing tree when attempting to write a commit
	ErrMissingTree = errors.New("missing tree")
	// ErrMissingCommitterName indicates an attempt to write a commit without a
	// committer name
	ErrMissingCommitterName = errors.New("missing committer name")
	// ErrMissingAuthorName indicates an attempt to write a commit without a
	// committer name
	ErrMissingAuthorName = errors.New("missing author name")
	// ErrDisallowedCharacters indicates the name and/or email contains disallowed
	// characters
	ErrDisallowedCharacters = errors.New("disallowed characters")
	// ErrObjectNotFound is returned in case an object could not be found.
	ErrObjectNotFound = errors.New("object not found")
)

type readCommitConfig struct {
	withTrailers bool
}

// ReadCommitOpt is an option for ReadCommit.
type ReadCommitOpt func(*readCommitConfig)

// WithTrailers will cause ReadCommit to parse commit trailers.
func WithTrailers() ReadCommitOpt {
	return func(cfg *readCommitConfig) {
		cfg.withTrailers = true
	}
}

// ReadCommit reads the commit specified by the given revision. If no such
// revision exists, it will return an ErrObjectNotFound error.
func (repo *Repo) ReadCommit(ctx context.Context, revision git.Revision, opts ...ReadCommitOpt) (*gitalypb.GitCommit, error) {
	var cfg readCommitConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	objectReader, cancel, err := repo.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, err
	}
	defer cancel()

	var commit *gitalypb.GitCommit
	if cfg.withTrailers {
		commit, err = catfile.GetCommitWithTrailers(ctx, repo.gitCmdFactory, repo, objectReader, revision)
	} else {
		var c *catfile.Commit
		if c, err = catfile.GetCommit(ctx, objectReader, revision); err == nil {
			commit = c.GitCommit
		}
	}

	if err != nil {
		if errors.As(err, &catfile.NotFoundError{}) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}

	return commit, nil
}

// WriteCommitConfig contains fields for writing a commit
type WriteCommitConfig struct {
	Reference          string
	Parents            []git.ObjectID
	AuthorDate         time.Time
	AuthorName         string
	AuthorEmail        string
	CommitterName      string
	CommitterEmail     string
	CommitterDate      time.Time
	Message            string
	TreeEntries        []TreeEntry
	TreeID             git.ObjectID
	AlternateObjectDir string
	Sign               bool
	GitConfig          config.Git
}

func validateWriteCommitConfig(cfg WriteCommitConfig) error {
	if cfg.TreeID == "" {
		return ErrMissingTree
	}

	if cfg.AuthorName == "" {
		return ErrMissingAuthorName
	}

	if cfg.CommitterName == "" {
		return ErrMissingCommitterName
	}

	return nil
}

// WriteCommit writes a new commit into the target repository.
func (repo *Repo) WriteCommit(ctx context.Context, cfg WriteCommitConfig) (git.ObjectID, error) {
	if err := validateWriteCommitConfig(cfg); err != nil {
		return "", err
	}

	if cfg.AuthorDate.IsZero() {
		cfg.AuthorDate = time.Now()
	}

	if cfg.CommitterDate.IsZero() {
		cfg.CommitterDate = time.Now()
	}

	// Use 'commit-tree' instead of 'commit' because we are in a bare
	// repository. What we do here is the same as "commit -m message
	// --allow-empty".
	commitArgs := []string{string(cfg.TreeID)}

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return "", fmt.Errorf("getting repo path: %w", err)
	}

	var env []string
	if cfg.AlternateObjectDir != "" {
		if !filepath.IsAbs(cfg.AlternateObjectDir) {
			return "", errors.New("alternate object directory must be an absolute path")
		}

		if err := os.MkdirAll(cfg.AlternateObjectDir, perm.SharedDir); err != nil {
			return "", err
		}

		env = append(env,
			fmt.Sprintf("GIT_OBJECT_DIRECTORY=%s", cfg.AlternateObjectDir),
			fmt.Sprintf("GIT_ALTERNATE_OBJECT_DIRECTORIES=%s", filepath.Join(repoPath, "objects")),
		)
	}

	env = append(env,
		fmt.Sprintf("GIT_AUTHOR_DATE=%s", git.FormatTime(cfg.AuthorDate)),
		fmt.Sprintf("GIT_AUTHOR_NAME=%s", cfg.AuthorName),
		fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", cfg.AuthorEmail),
		fmt.Sprintf("GIT_COMMITTER_DATE=%s", git.FormatTime(cfg.CommitterDate)),
	)

	if featureflag.GPGSigning.IsEnabled(ctx) && cfg.Sign && cfg.GitConfig.CommitterName != "" && cfg.GitConfig.CommitterEmail != "" {
		env = append(env,
			fmt.Sprintf("GIT_COMMITTER_NAME=%s", cfg.GitConfig.CommitterName),
			fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", cfg.GitConfig.CommitterEmail),
		)
	} else {
		env = append(env,
			fmt.Sprintf("GIT_COMMITTER_NAME=%s", cfg.CommitterName),
			fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", cfg.CommitterEmail),
		)
	}

	var flags []git.Option

	for _, parent := range cfg.Parents {
		flags = append(flags, git.ValueFlag{Name: "-p", Value: parent.String()})
	}

	flags = append(flags, git.ValueFlag{Name: "-F", Value: "-"})

	var stdout, stderr bytes.Buffer

	opts := []git.CmdOpt{
		git.WithStdout(&stdout),
		git.WithStderr(&stderr),
		git.WithStdin(strings.NewReader(cfg.Message)),
		git.WithEnv(env...),
	}

	if featureflag.GPGSigning.IsEnabled(ctx) && cfg.Sign && cfg.GitConfig.SigningKey != "" {
		flags = append(flags, git.Flag{Name: "--gpg-sign=" + cfg.GitConfig.SigningKey})
		opts = append(opts, git.WithGitalyGPG())
	}

	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "commit-tree",
			Flags: flags,
			Args:  commitArgs,
		},
		opts...,
	); err != nil {
		if strings.Contains(stderr.String(), "name consists only of disallowed characters") {
			return "", ErrDisallowedCharacters
		}

		return "", fmt.Errorf("commit-tree: %w: %s %s", err, stderr.String(), stdout.String())
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting object hash: %w", err)
	}

	oid, err := objectHash.FromHex(text.ChompBytes(stdout.Bytes()))
	if err != nil {
		return "", fmt.Errorf("hex to object hash: %w", err)
	}

	if cfg.Reference != "" {
		if err := repo.UpdateRef(
			ctx,
			git.ReferenceName(cfg.Reference),
			oid,
			"",
		); err != nil {
			return "", fmt.Errorf("updating ref: %w", err)
		}
	}

	return oid, nil
}

// InvalidCommitError is returned when the revision does not point to a valid commit object.
type InvalidCommitError git.Revision

func (err InvalidCommitError) Error() string {
	return fmt.Sprintf("invalid commit: %q", string(err))
}

// IsAncestor returns whether the parent is an ancestor of the child. InvalidCommitError is returned
// if either revision does not point to a commit in the repository.
func (repo *Repo) IsAncestor(ctx context.Context, parent, child git.Revision) (bool, error) {
	const notValidCommitName = "fatal: Not a valid commit name"

	stderr := &bytes.Buffer{}
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "merge-base",
			Flags: []git.Option{git.Flag{Name: "--is-ancestor"}},
			Args:  []string{parent.String(), child.String()},
		},
		git.WithStderr(stderr),
	); err != nil {
		status, ok := command.ExitStatus(err)
		if ok && status == 1 {
			return false, nil
		} else if ok && strings.HasPrefix(stderr.String(), notValidCommitName) {
			commitOID := strings.TrimSpace(strings.TrimPrefix(stderr.String(), notValidCommitName))
			return false, InvalidCommitError(commitOID)
		}

		return false, fmt.Errorf("determine ancestry: %w, stderr: %q", err, stderr)
	}

	return true, nil
}
