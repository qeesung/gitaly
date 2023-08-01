package localrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
)

// HasRevision checks if a revision in the repository exists. This will not
// verify whether the target object exists. To do so, you can peel the revision
// to a given object type, e.g. by passing `refs/heads/master^{commit}`.
func (repo *Repo) HasRevision(ctx context.Context, revision git.Revision) (bool, error) {
	if _, err := repo.ResolveRevision(ctx, revision); err != nil {
		if errors.Is(err, git.ErrReferenceNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ResolveRevision resolves the given revision to its object ID. This will not
// verify whether the target object exists. To do so, you can peel the
// reference to a given object type, e.g. by passing
// `refs/heads/master^{commit}`. Returns an ErrReferenceNotFound error in case
// the revision does not exist.
func (repo *Repo) ResolveRevision(ctx context.Context, revision git.Revision) (git.ObjectID, error) {
	if revision.String() == "" {
		return "", errors.New("repository cannot contain empty reference name")
	}

	var stdout bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "rev-parse",
			Flags: []git.Option{git.Flag{Name: "--verify"}},
			Args:  []string{revision.String()},
		},
		git.WithStderr(io.Discard),
		git.WithStdout(&stdout),
	); err != nil {
		if _, ok := command.ExitStatus(err); ok {
			return "", git.ErrReferenceNotFound
		}
		return "", err
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting object hash: %w", err)
	}

	hex := strings.TrimSpace(stdout.String())
	oid, err := objectHash.FromHex(hex)
	if err != nil {
		return "", fmt.Errorf("unsupported object hash %q: %w", hex, err)
	}

	return oid, nil
}

// GetReference looks up and returns the given reference. Returns a
// ReferenceNotFound error if the reference was not found.
func (repo *Repo) GetReference(ctx context.Context, reference git.ReferenceName) (git.Reference, error) {
	refs, err := git.GetReferences(ctx, repo, git.GetReferencesConfig{
		Patterns: []string{reference.String()},
		Limit:    1,
	})
	if err != nil {
		return git.Reference{}, err
	}

	if len(refs) == 0 {
		return git.Reference{}, git.ErrReferenceNotFound
	}
	if refs[0].Name != reference {
		return git.Reference{}, fmt.Errorf("%w: conflicts with %q", git.ErrReferenceAmbiguous, refs[0].Name)
	}

	return refs[0], nil
}

// HasBranches determines whether there is at least one branch in the
// repository.
func (repo *Repo) HasBranches(ctx context.Context) (bool, error) {
	refs, err := git.GetReferences(ctx, repo, git.GetReferencesConfig{
		Patterns: []string{"refs/heads/"},
		Limit:    1,
	})
	return len(refs) > 0, err
}

// GetReferences returns references matching any of the given patterns. If no patterns are given,
// all references are returned.
func (repo *Repo) GetReferences(ctx context.Context, patterns ...string) ([]git.Reference, error) {
	return git.GetReferences(ctx, repo, git.GetReferencesConfig{
		Patterns: patterns,
	})
}

// GetBranches returns all branches.
func (repo *Repo) GetBranches(ctx context.Context) ([]git.Reference, error) {
	return repo.GetReferences(ctx, "refs/heads/")
}

// UpdateRef updates reference from oldValue to newValue. If oldValue is a
// non-empty string, the update will fail it the reference is not currently at
// that revision. If newValue is the ZeroOID, the reference will be deleted.
// If oldValue is the ZeroOID, the reference will created.
func (repo *Repo) UpdateRef(ctx context.Context, reference git.ReferenceName, newValue, oldValue git.ObjectID) error {
	var stderr bytes.Buffer

	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name:  "update-ref",
			Flags: []git.Option{git.Flag{Name: "-z"}, git.Flag{Name: "--stdin"}},
		},
		git.WithStdin(strings.NewReader(fmt.Sprintf("update %s\x00%s\x00%s\x00", reference, newValue.String(), oldValue.String()))),
		git.WithStderr(&stderr),
		git.WithRefTxHook(repo),
	); err != nil {
		return fmt.Errorf("UpdateRef: failed updating reference %q from %q to %q: %w", reference, oldValue, newValue, errorWithStderr(err, stderr.Bytes()))
	}

	return nil
}

// SetDefaultBranch sets the repository's HEAD to point to the given reference.
// It will not verify the reference actually exists.
func (repo *Repo) SetDefaultBranch(ctx context.Context, txManager transaction.Manager, reference git.ReferenceName) error {
	return repo.setDefaultBranchWithTransaction(ctx, txManager, reference)
}

// setDefaultBranchWithTransaction sets the repository's HEAD to point to the given reference
// using a safe locking file writer and commits the transaction if one exists in the context
func (repo *Repo) setDefaultBranchWithTransaction(ctx context.Context, txManager transaction.Manager, reference git.ReferenceName) error {
	if err := git.ValidateReference(reference.String()); err != nil {
		return fmt.Errorf("%q is a malformed refname", reference)
	}

	newHeadContent := []byte(fmt.Sprintf("ref: %s\n", reference.String()))

	repoPath, err := repo.Path()
	if err != nil {
		return fmt.Errorf("getting repository path: %w", err)
	}

	headPath := filepath.Join(repoPath, "HEAD")

	lockingFileWriter, err := safe.NewLockingFileWriter(headPath)
	if err != nil {
		return fmt.Errorf("getting file writer: %w", err)
	}
	defer func() {
		if err := lockingFileWriter.Close(); err != nil {
			repo.logger.WithError(err).ErrorContext(ctx, "closing locked HEAD failed")
		}
	}()

	if _, err := lockingFileWriter.Write(newHeadContent); err != nil {
		return fmt.Errorf("writing temporary HEAD: %w", err)
	}

	if err := transaction.CommitLockedFile(ctx, txManager, lockingFileWriter); err != nil {
		return fmt.Errorf("committing temporary HEAD: %w", err)
	}

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		tx.SetDefaultBranch(reference)
	})

	return nil
}

type getRemoteReferenceConfig struct {
	patterns   []string
	config     []git.ConfigPair
	sshCommand string
}

// GetRemoteReferencesOption is an option which can be passed to GetRemoteReferences.
type GetRemoteReferencesOption func(*getRemoteReferenceConfig)

// WithPatterns sets up a set of patterns which are then used to filter the list of returned
// references.
func WithPatterns(patterns ...string) GetRemoteReferencesOption {
	return func(cfg *getRemoteReferenceConfig) {
		cfg.patterns = patterns
	}
}

// WithSSHCommand sets the SSH invocation to use when communicating with the remote.
func WithSSHCommand(cmd string) GetRemoteReferencesOption {
	return func(cfg *getRemoteReferenceConfig) {
		cfg.sshCommand = cmd
	}
}

// WithConfig sets up Git configuration for the git-ls-remote(1) invocation. The config pairs are
// set up via `WithConfigEnv()` and are thus not exposed via the command line.
func WithConfig(config ...git.ConfigPair) GetRemoteReferencesOption {
	return func(cfg *getRemoteReferenceConfig) {
		cfg.config = config
	}
}

// GetRemoteReferences lists references of the remote. Peeled tags are not returned.
func (repo *Repo) GetRemoteReferences(ctx context.Context, remote string, opts ...GetRemoteReferencesOption) ([]git.Reference, error) {
	var cfg getRemoteReferenceConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	var env []string
	if cfg.sshCommand != "" {
		env = append(env, envGitSSHCommand(cfg.sshCommand))
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "ls-remote",
			Flags: []git.Option{
				git.Flag{Name: "--refs"},
				git.Flag{Name: "--symref"},
			},
			Args: append([]string{remote}, cfg.patterns...),
		},
		git.WithStdout(stdout),
		git.WithStderr(stderr),
		git.WithEnv(env...),
		git.WithConfigEnv(cfg.config...),
	); err != nil {
		return nil, fmt.Errorf("create git ls-remote: %w, stderr: %q", err, stderr)
	}

	var refs []git.Reference
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		split := strings.SplitN(scanner.Text(), "\t", 2)
		if len(split) != 2 {
			return nil, fmt.Errorf("invalid ls-remote output line: %q", scanner.Text())
		}

		// Symbolic references are outputted as:
		//	ref: refs/heads/master	refs/heads/symbolic-ref
		//	0c9cf732b5774fa948348bbd6f273009bd66e04c	refs/heads/symbolic-ref
		if strings.HasPrefix(split[0], "ref: ") {
			symRef := split[1]
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return nil, fmt.Errorf("scan dereferenced symbolic ref: %w", err)
				}

				return nil, fmt.Errorf("missing dereferenced symbolic ref line for %q", symRef)
			}

			split = strings.SplitN(scanner.Text(), "\t", 2)
			if len(split) != 2 {
				return nil, fmt.Errorf("invalid dereferenced symbolic ref line: %q", scanner.Text())
			}

			if split[1] != symRef {
				return nil, fmt.Errorf("expected dereferenced symbolic ref %q but got reference %q", symRef, split[1])
			}

			refs = append(refs, git.NewSymbolicReference(git.ReferenceName(symRef), git.ReferenceName(split[0])))
			continue
		}

		refs = append(refs, git.NewReference(git.ReferenceName(split[1]), git.ObjectID(split[0])))
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return refs, nil
}

// GetDefaultBranch determines the default branch name
func (repo *Repo) GetDefaultBranch(ctx context.Context) (git.ReferenceName, error) {
	headReference, err := repo.HeadReference(ctx)
	if err != nil {
		return "", err
	}

	// Ideally we would only use HEAD to determine the default branch, but
	// gitlab-rails depends on the branch being determined like this.
	for _, headCandidate := range []git.ReferenceName{
		headReference, git.DefaultRef, git.LegacyDefaultRef,
	} {
		has, err := repo.HasRevision(ctx, headCandidate.Revision())
		if err != nil {
			return "", err
		}
		if has {
			return headCandidate, nil
		}
	}

	// If all else fails, return the first branch name
	branches, err := git.GetReferences(ctx, repo, git.GetReferencesConfig{
		Patterns: []string{"refs/heads/"},
		Limit:    1,
	})
	if err != nil || len(branches) == 0 {
		return "", err
	}

	return branches[0].Name, nil
}

// HeadReference returns the current value of HEAD.
func (repo *Repo) HeadReference(ctx context.Context) (git.ReferenceName, error) {
	symref, err := git.GetSymbolicRef(ctx, repo, "HEAD")
	if err != nil {
		return "", err
	}

	return git.ReferenceName(symref.Target), nil
}

// GuessHead tries to guess what branch HEAD would be pointed at. If no
// reference is found git.ErrReferenceNotFound is returned.
//
// This function should be roughly equivalent to the corresponding function in
// git:
// https://github.com/git/git/blob/2a97289ad8b103625d3a1a12f66c27f50df822ce/remote.c#L2198
func (repo *Repo) GuessHead(ctx context.Context, head git.Reference) (git.ReferenceName, error) {
	if head.IsSymbolic {
		return git.ReferenceName(head.Target), nil
	}

	// Try current and historic default branches first. Ideally we might look
	// up the git config `init.defaultBranch` but we do not allow this
	// configuration to be set by the user. It is always set to
	// `git.DefaultRef`.
	for _, name := range []git.ReferenceName{git.DefaultRef, git.LegacyDefaultRef} {
		ref, err := repo.GetReference(ctx, name)
		switch {
		case errors.Is(err, git.ErrReferenceNotFound):
			continue
		case err != nil:
			return "", fmt.Errorf("guess head: default: %w", err)
		case head.Target == ref.Target:
			return ref.Name, nil
		}
	}

	refs, err := repo.GetBranches(ctx)
	if err != nil {
		return "", fmt.Errorf("guess head: %w", err)
	}
	for _, ref := range refs {
		if ref.Target != head.Target {
			continue
		}
		return ref.Name, nil
	}

	return "", fmt.Errorf("guess head: %w", git.ErrReferenceNotFound)
}
