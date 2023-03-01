package gittest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

// GetRepositoryFunc is used to get a clean test repository for the different implementations of the
// Repository interface in the common test suite TestRepository.
type GetRepositoryFunc func(t testing.TB, ctx context.Context) (git.Repository, string)

// TestRepository tests an implementation of Repository.
func TestRepository(t *testing.T, cfg config.Cfg, getRepository GetRepositoryFunc) {
	for _, tc := range []struct {
		desc string
		test func(*testing.T, context.Context, config.Cfg, GetRepositoryFunc)
	}{
		{
			desc: "ResolveRevision",
			test: testRepositoryResolveRevision,
		},
		{
			desc: "HasBranches",
			test: testRepositoryHasBranches,
		},
		{
			desc: "GetDefaultBranch",
			test: testRepositoryGetDefaultBranch,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			testhelper.NewFeatureSets(featureflag.HeadAsDefaultBranch).Run(t, func(t *testing.T, ctx context.Context) {
				tc.test(t, ctx, cfg, getRepository)
			})
		})
	}
}

func testRepositoryResolveRevision(t *testing.T, ctx context.Context, cfg config.Cfg, getRepository GetRepositoryFunc) {
	repo, repoPath := getRepository(t, ctx)

	firstParentCommitID := WriteCommit(t, cfg, repoPath, WithMessage("first parent"))
	secondParentCommitID := WriteCommit(t, cfg, repoPath, WithMessage("second parent"))
	masterCommitID := WriteCommit(t, cfg, repoPath, WithBranch("master"), WithParents(firstParentCommitID, secondParentCommitID))

	for _, tc := range []struct {
		desc     string
		revision string
		expected git.ObjectID
	}{
		{
			desc:     "unqualified master branch",
			revision: "master",
			expected: masterCommitID,
		},
		{
			desc:     "fully qualified master branch",
			revision: "refs/heads/master",
			expected: masterCommitID,
		},
		{
			desc:     "typed commit",
			revision: "refs/heads/master^{commit}",
			expected: masterCommitID,
		},
		{
			desc:     "extended SHA notation",
			revision: "refs/heads/master^2",
			expected: secondParentCommitID,
		},
		{
			desc:     "nonexistent branch",
			revision: "refs/heads/foobar",
		},
		{
			desc:     "SHA notation gone wrong",
			revision: "refs/heads/master^3",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid, err := repo.ResolveRevision(ctx, git.Revision(tc.revision))
			if tc.expected == "" {
				require.Equal(t, err, git.ErrReferenceNotFound)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expected, oid)
		})
	}
}

func testRepositoryHasBranches(t *testing.T, ctx context.Context, cfg config.Cfg, getRepository GetRepositoryFunc) {
	repo, repoPath := getRepository(t, ctx)

	emptyCommit := text.ChompBytes(Exec(t, cfg, "-C", repoPath, "commit-tree", DefaultObjectHash.EmptyTreeOID.String()))

	Exec(t, cfg, "-C", repoPath, "update-ref", "refs/headsbranch", emptyCommit)

	hasBranches, err := repo.HasBranches(ctx)
	require.NoError(t, err)
	require.False(t, hasBranches)

	Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch", emptyCommit)

	hasBranches, err = repo.HasBranches(ctx)
	require.NoError(t, err)
	require.True(t, hasBranches)
}

func testRepositoryGetDefaultBranch(t *testing.T, ctx context.Context, cfg config.Cfg, getRepository GetRepositoryFunc) {
	for _, tc := range []struct {
		desc         string
		repo         func(t *testing.T) git.Repository
		expectedName git.ReferenceName
	}{
		{
			desc: "default ref",
			repo: func(t *testing.T) git.Repository {
				repo, repoPath := getRepository(t, ctx)
				oid := WriteCommit(t, cfg, repoPath, WithBranch("apple"))
				WriteCommit(t, cfg, repoPath, WithParents(oid), WithBranch("main"))
				return repo
			},
			expectedName: git.DefaultRef,
		},
		{
			desc: "legacy default ref",
			repo: func(t *testing.T) git.Repository {
				repo, repoPath := getRepository(t, ctx)
				oid := WriteCommit(t, cfg, repoPath, WithBranch("apple"))
				WriteCommit(t, cfg, repoPath, WithParents(oid), WithBranch("master"))
				return repo
			},
			expectedName: testhelper.EnabledOrDisabledFlag(ctx, featureflag.HeadAsDefaultBranch,
				git.DefaultRef,
				git.LegacyDefaultRef,
			),
		},
		{
			desc: "no branches",
			repo: func(t *testing.T) git.Repository {
				repo, _ := getRepository(t, ctx)
				return repo
			},
			expectedName: testhelper.EnabledOrDisabledFlag(ctx, featureflag.HeadAsDefaultBranch,
				git.DefaultRef,
				git.ReferenceName(""),
			),
		},
		{
			desc: "one branch",
			repo: func(t *testing.T) git.Repository {
				repo, repoPath := getRepository(t, ctx)
				WriteCommit(t, cfg, repoPath, WithBranch("apple"))
				return repo
			},
			expectedName: testhelper.EnabledOrDisabledFlag(ctx, featureflag.HeadAsDefaultBranch,
				git.DefaultRef,
				git.NewReferenceNameFromBranchName("apple"),
			),
		},
		{
			desc: "no default branches",
			repo: func(t *testing.T) git.Repository {
				repo, repoPath := getRepository(t, ctx)
				oid := WriteCommit(t, cfg, repoPath, WithBranch("apple"))
				WriteCommit(t, cfg, repoPath, WithParents(oid), WithBranch("banana"))
				return repo
			},
			expectedName: testhelper.EnabledOrDisabledFlag(ctx, featureflag.HeadAsDefaultBranch,
				git.DefaultRef,
				git.NewReferenceNameFromBranchName("apple"),
			),
		},
		{
			desc: "test repo HEAD set",
			repo: func(t *testing.T) git.Repository {
				repo, repoPath := getRepository(t, ctx)

				WriteCommit(t, cfg, repoPath, WithBranch("feature"))
				Exec(t, cfg, "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/feature")

				return repo
			},
			expectedName: git.NewReferenceNameFromBranchName("feature"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			name, err := tc.repo(t).GetDefaultBranch(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedName, name)
		})
	}
}
