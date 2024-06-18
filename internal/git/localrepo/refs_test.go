package localrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRepo_ContainsRef(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

	testcases := []struct {
		desc      string
		ref       string
		contained bool
	}{
		{
			desc:      "unqualified master branch",
			ref:       "master",
			contained: true,
		},
		{
			desc:      "fully qualified master branch",
			ref:       "refs/heads/master",
			contained: true,
		},
		{
			desc:      "nonexistent branch",
			ref:       "refs/heads/nonexistent",
			contained: false,
		},
	}

	for _, tc := range testcases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			contained, err := repo.HasRevision(ctx, git.Revision(tc.ref))
			require.NoError(t, err)
			require.Equal(t, tc.contained, contained)
		})
	}
}

func TestRepo_ResolveRevision(t *testing.T) {
	t.Parallel()

	testcases := []struct {
		desc        string
		setup       func(t *testing.T, cfg config.Cfg, repoPath string) git.ObjectID
		revision    string
		expectedErr error
	}{
		{
			desc: "branch",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.ObjectID {
				return gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
			},
			revision: "main",
		},
		{
			desc: "tag",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.ObjectID {
				return gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/tags/1.0.0"))
			},
			revision: "1.0.0",
		},
		{
			desc: "branch parent",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.ObjectID {
				parent := gittest.WriteCommit(t, cfg, repoPath)

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(parent),
					gittest.WithBranch("main"),
				)

				return parent
			},
			revision: "main^",
		},
		{
			desc: "peel branch to tree",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.ObjectID {
				treeID := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "foo.bar", Mode: "100644", Content: "Hello world"},
					})

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(treeID),
					gittest.WithBranch("main"))

				return treeID
			},
			revision: "main^{tree}",
		},
	}
	for _, tc := range testcases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg, repo, repoPath := setupRepo(t)
			expectedID := tc.setup(t, cfg, repoPath)

			ref, err := repo.ResolveRevision(ctx, git.Revision(tc.revision))
			require.Equal(t, tc.expectedErr, err)
			if err == nil {
				require.Equal(t, expectedID, ref)
			}
		})
	}
}

func TestRepo_GetReference(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

	testcases := []struct {
		desc        string
		ref         string
		expected    git.Reference
		expectedErr error
	}{
		{
			desc:     "fully qualified master branch",
			ref:      "refs/heads/master",
			expected: git.NewReference("refs/heads/master", commitID),
		},
		{
			desc:        "unqualified master branch fails",
			ref:         "master",
			expectedErr: git.ErrReferenceNotFound,
		},
		{
			desc:        "nonexistent branch",
			ref:         "refs/heads/nonexistent",
			expectedErr: git.ErrReferenceNotFound,
		},
		{
			desc:        "prefix returns an error",
			ref:         "refs/heads",
			expectedErr: fmt.Errorf("%w: conflicts with %q", git.ErrReferenceAmbiguous, "refs/heads/master"),
		},
		{
			desc:        "nonexistent branch",
			ref:         "nonexistent",
			expectedErr: git.ErrReferenceNotFound,
		},
	}

	for _, tc := range testcases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ref, err := repo.GetReference(ctx, git.ReferenceName(tc.ref))
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expected, ref)
		})
	}
}

func TestRepo_GetReferenceWithAmbiguousRefs(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t, withDisabledHooks())

	prevOID := gittest.WriteCommit(t, cfg, repoPath)
	currentOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(prevOID), gittest.WithBranch("master"))

	for _, ref := range []git.ReferenceName{
		"refs/heads/something/master",
		"refs/heads/master2",
		"refs/heads/masterx",
		"refs/heads/refs/heads/master",
		"refs/heads/heads/master",
		"refs/master",
		"refs/tags/master",
	} {
		require.NoError(t, repo.UpdateRef(ctx, ref, prevOID, gittest.DefaultObjectHash.ZeroOID))
	}

	// core.ignorecase is default-enabled on macOS, causing 'MASTER' to match 'master'
	if runtime.GOOS != "darwin" {
		require.NoError(t, repo.UpdateRef(ctx, "refs/heads/MASTER", prevOID, gittest.DefaultObjectHash.ZeroOID))
	}

	ref, err := repo.GetReference(ctx, "refs/heads/master")
	require.NoError(t, err)
	require.Equal(t, git.Reference{
		Name:   "refs/heads/master",
		Target: currentOID.String(),
	}, ref)
}

func TestRepo_GetReferences(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"))
	gittest.WriteTag(t, cfg, repoPath, "v1.0.0", "refs/heads/main")

	mainReference, err := repo.GetReference(ctx, "refs/heads/main")
	require.NoError(t, err)
	featureReference, err := repo.GetReference(ctx, "refs/heads/feature")
	require.NoError(t, err)
	tagReference, err := repo.GetReference(ctx, "refs/tags/v1.0.0")
	require.NoError(t, err)

	testcases := []struct {
		desc         string
		patterns     []string
		expectedRefs []git.Reference
	}{
		{
			desc:     "main branch",
			patterns: []string{"refs/heads/main"},
			expectedRefs: []git.Reference{
				mainReference,
			},
		},
		{
			desc:     "two branches",
			patterns: []string{"refs/heads/main", "refs/heads/feature"},
			expectedRefs: []git.Reference{
				featureReference,
				mainReference,
			},
		},
		{
			desc:     "matching subset is returned",
			patterns: []string{"refs/heads/main", "refs/heads/nonexistent"},
			expectedRefs: []git.Reference{
				mainReference,
			},
		},
		{
			desc: "all references",
			expectedRefs: []git.Reference{
				featureReference,
				mainReference,
				tagReference,
			},
		},
		{
			desc:     "branches",
			patterns: []string{"refs/heads/"},
			expectedRefs: []git.Reference{
				featureReference,
				mainReference,
			},
		},
		{
			desc:     "non-existent branch",
			patterns: []string{"refs/heads/nonexistent"},
		},
	}

	for _, tc := range testcases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			refs, err := repo.GetReferences(ctx, tc.patterns...)
			require.NoError(t, err)
			require.Equal(t, tc.expectedRefs, refs)
		})
	}
}

func TestRepo_GetRemoteReferences(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	gitCmdFactory, readSSHCommand := captureGitSSHCommand(t, ctx, cfg)

	storagePath, ok := cfg.StoragePath("default")
	require.True(t, ok)

	const relativePath = "repository-1"
	repoPath := filepath.Join(storagePath, relativePath)

	gittest.Exec(t, cfg, "init", repoPath)
	gittest.Exec(t, cfg, "-C", repoPath, "commit", "--allow-empty", "-m", "commit message")
	commit := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", git.DefaultRef.String()))

	for _, cmd := range [][]string{
		{"update-ref", git.DefaultRef.String(), commit},
		{"tag", "lightweight-tag", commit},
		{"tag", "-m", "tag message", "annotated-tag", git.DefaultRef.String()},
		{"symbolic-ref", "refs/heads/symbolic", git.DefaultRef.String()},
		{"update-ref", "refs/remote/remote-name/remote-branch", commit},
	} {
		gittest.Exec(t, cfg, append([]string{"-C", repoPath}, cmd...)...)
	}

	annotatedTagOID := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "annotated-tag"))

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	repo := New(
		testhelper.NewLogger(t),
		config.NewLocator(cfg),
		gitCmdFactory,
		catfileCache,
		&gitalypb.Repository{StorageName: "default", RelativePath: filepath.Join(relativePath, ".git")},
	)
	for _, tc := range []struct {
		desc                  string
		remote                string
		opts                  []GetRemoteReferencesOption
		expected              []git.Reference
		expectedGitSSHCommand string
	}{
		{
			desc:   "not found",
			remote: repoPath,
			opts: []GetRemoteReferencesOption{
				WithPatterns("this-pattern-does-not-match-anything"),
			},
		},
		{
			desc:   "all",
			remote: repoPath,
			expected: []git.Reference{
				{Name: git.DefaultRef, Target: commit},
				{Name: "refs/heads/symbolic", Target: commit, IsSymbolic: true},
				{Name: "refs/remote/remote-name/remote-branch", Target: commit},
				{Name: "refs/tags/annotated-tag", Target: annotatedTagOID},
				{Name: "refs/tags/lightweight-tag", Target: commit},
			},
		},
		{
			desc:   "branches and tags only",
			remote: repoPath,
			opts: []GetRemoteReferencesOption{
				WithPatterns("refs/heads/*", "refs/tags/*"),
			},
			expected: []git.Reference{
				{Name: git.DefaultRef, Target: commit},
				{Name: "refs/heads/symbolic", Target: commit, IsSymbolic: true},
				{Name: "refs/tags/annotated-tag", Target: annotatedTagOID},
				{Name: "refs/tags/lightweight-tag", Target: commit},
			},
		},
		{
			desc:   "with in-memory remote",
			remote: "inmemory",
			opts: []GetRemoteReferencesOption{
				WithPatterns(git.DefaultRef.String()),
				WithConfig(git.ConfigPair{
					Key:   "remote.inmemory.url",
					Value: repoPath,
				}),
			},
			expected: []git.Reference{
				{Name: git.DefaultRef, Target: commit},
			},
		},
		{
			desc:   "with custom ssh command",
			remote: repoPath,
			opts: []GetRemoteReferencesOption{
				WithPatterns(git.DefaultRef.String()),
				WithSSHCommand("custom-ssh -with-creds"),
			},
			expected: []git.Reference{
				{Name: git.DefaultRef, Target: commit},
			},
			expectedGitSSHCommand: "custom-ssh -with-creds",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			refs, err := repo.GetRemoteReferences(ctx, tc.remote, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.expected, refs)

			gitSSHCommand, err := readSSHCommand()
			if !os.IsNotExist(err) {
				require.NoError(t, err)
			}

			require.Equal(t, tc.expectedGitSSHCommand, string(gitSSHCommand))
		})
	}
}

func TestRepo_GetBranches(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t)

	mainID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
	featureID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))
	thirdID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("third"), gittest.WithMessage("third"))

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/different/namespace"))
	gittest.WriteTag(t, cfg, repoPath, "v1.0.0", mainID.Revision())

	refs, err := repo.GetBranches(ctx)
	require.NoError(t, err)
	require.Equal(t, []git.Reference{
		{Name: "refs/heads/feature", Target: featureID.String()},
		{Name: "refs/heads/main", Target: mainID.String()},
		{Name: "refs/heads/third", Target: thirdID.String()},
	}, refs)
}

func TestRepo_UpdateRef(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, repoPath := setupRepo(t, withDisabledHooks())

	// We move this into a function so that we can re-seed the repository for each test.
	seedRepo := func(t *testing.T, repoPath string) {
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("other"), gittest.WithMessage("other"))
	}
	seedRepo(t, repoPath)

	mainCommitID := gittest.ResolveRevision(t, cfg, repoPath, "refs/heads/main")
	otherRef, err := repo.GetReference(ctx, "refs/heads/other")
	require.NoError(t, err)

	nonexistentOID := git.ObjectID(strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen()))

	testcases := []struct {
		desc     string
		ref      string
		newValue git.ObjectID
		oldValue git.ObjectID
		verify   func(t *testing.T, repo *Repo, err error)
	}{
		{
			desc:     "successfully update main",
			ref:      "refs/heads/main",
			newValue: git.ObjectID(otherRef.Target),
			oldValue: mainCommitID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.NoError(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/main")
				require.NoError(t, err)
				require.Equal(t, ref.Target, otherRef.Target)
			},
		},
		{
			desc:     "update fails with stale oldValue",
			ref:      "refs/heads/main",
			newValue: git.ObjectID(otherRef.Target),
			oldValue: nonexistentOID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.Error(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/main")
				require.NoError(t, err)
				require.Equal(t, ref.Target, mainCommitID.String())
			},
		},
		{
			desc:     "update fails with invalid newValue",
			ref:      "refs/heads/main",
			newValue: nonexistentOID,
			oldValue: mainCommitID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.Error(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/main")
				require.NoError(t, err)
				require.Equal(t, ref.Target, mainCommitID.String())
			},
		},
		{
			desc:     "successfully update main with empty oldValue",
			ref:      "refs/heads/main",
			newValue: git.ObjectID(otherRef.Target),
			oldValue: "",
			verify: func(t *testing.T, repo *Repo, err error) {
				require.NoError(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/main")
				require.NoError(t, err)
				require.Equal(t, ref.Target, otherRef.Target)
			},
		},
		{
			desc:     "updating unqualified branch fails",
			ref:      "main",
			newValue: git.ObjectID(otherRef.Target),
			oldValue: mainCommitID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.Error(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/main")
				require.NoError(t, err)
				require.Equal(t, ref.Target, mainCommitID.String())
			},
		},
		{
			desc:     "deleting main succeeds",
			ref:      "refs/heads/main",
			newValue: gittest.DefaultObjectHash.ZeroOID,
			oldValue: mainCommitID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.NoError(t, err)
				_, err = repo.GetReference(ctx, "refs/heads/main")
				require.Error(t, err)
			},
		},
		{
			desc:     "creating new branch succeeds",
			ref:      "refs/heads/new",
			newValue: mainCommitID,
			oldValue: gittest.DefaultObjectHash.ZeroOID,
			verify: func(t *testing.T, repo *Repo, err error) {
				require.NoError(t, err)
				ref, err := repo.GetReference(ctx, "refs/heads/new")
				require.NoError(t, err)
				require.Equal(t, ref.Target, mainCommitID.String())
			},
		},
	}

	for _, tc := range testcases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			// We need to create the repository every time so that we don't carry over
			// the state.
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := New(repo.logger, repo.locator, repo.gitCmdFactory, repo.catfileCache, repoProto)
			seedRepo(t, repoPath)

			err := repo.UpdateRef(ctx, git.ReferenceName(tc.ref), tc.newValue, tc.oldValue)
			tc.verify(t, repo, err)
		})
	}
}

func TestGuessHead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc        string
		setup       func(*testing.T, config.Cfg, string) git.Reference
		expected    git.ReferenceName
		expectedErr error
	}{
		{
			desc: "symbolic",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				return git.NewSymbolicReference("HEAD", "refs/heads/something")
			},
			expected: "refs/heads/something",
		},
		{
			desc: "matching default branch",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.DefaultRef.String(), commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.LegacyDefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/apple", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/feature", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/zucchini", commit1.String())

				return git.NewReference("HEAD", commit1)
			},
			expected: git.DefaultRef,
		},
		{
			desc: "matching default legacy branch",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.DefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.LegacyDefaultRef.String(), commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/apple", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/feature", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/zucchini", commit1.String())

				return git.NewReference("HEAD", commit1)
			},
			expected: git.LegacyDefaultRef,
		},
		{
			desc: "matching other branch",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.DefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.LegacyDefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/apple", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/feature", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/zucchini", commit1.String())
				return git.NewReference("HEAD", commit1)
			},
			expected: "refs/heads/apple",
		},
		{
			desc: "missing default branches",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "-d", git.DefaultRef.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "-d", git.LegacyDefaultRef.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/apple", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/feature", commit1.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/zucchini", commit1.String())
				return git.NewReference("HEAD", commit1)
			},
			expected: "refs/heads/apple",
		},
		{
			desc: "no match",
			setup: func(t *testing.T, cfg config.Cfg, repoPath string) git.Reference {
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.DefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", git.LegacyDefaultRef.String(), commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/apple", commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/feature", commit2.String())
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/zucchini", commit2.String())

				return git.NewReference("HEAD", commit1)
			},
			expectedErr: fmt.Errorf("guess head: %w", git.ErrReferenceNotFound),
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg, repo, repoPath := setupRepo(t)

			head := tc.setup(t, cfg, repoPath)

			guess, err := repo.GuessHead(ctx, head)
			if tc.expectedErr == nil {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.expectedErr, err)
			}
			require.Equal(t, tc.expected, guess)
		})
	}
}
