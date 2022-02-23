package ref

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPackRefsSuccessfulRequest(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repoProto, repoPath, client := setupRefService(ctx, t)

	packedRefs := linesInPackfile(t, repoPath)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// creates some new heads
	newBranches := 10
	for i := 0; i < newBranches; i++ {
		require.NoError(t, repo.UpdateRef(ctx, git.ReferenceName(fmt.Sprintf("refs/heads/new-ref-%d", i)), "refs/heads/master", git.ZeroOID))
	}

	// pack all refs
	//nolint:staticcheck
	_, err := client.PackRefs(ctx, &gitalypb.PackRefsRequest{Repository: repoProto})
	require.NoError(t, err)

	files, err := os.ReadDir(filepath.Join(repoPath, "refs/heads"))
	require.NoError(t, err)
	assert.Len(t, files, 0, "git pack-refs --all should have packed all refs in refs/heads")
	assert.Equal(t, packedRefs+newBranches, linesInPackfile(t, repoPath), fmt.Sprintf("should have added %d new lines to the packfile", newBranches))

	// ensure all refs are reachable
	for i := 0; i < newBranches; i++ {
		gittest.Exec(t, cfg, "-C", repoPath, "show-ref", fmt.Sprintf("refs/heads/new-ref-%d", i))
	}
}

func linesInPackfile(t *testing.T, repoPath string) int {
	packFile, err := os.Open(filepath.Join(repoPath, "packed-refs"))
	require.NoError(t, err)
	defer packFile.Close()
	scanner := bufio.NewScanner(packFile)
	var refs int
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "#") {
			continue
		}
		refs++
	}
	return refs
}

func TestPackRefs_invalidRequest(t *testing.T) {
	t.Parallel()

	cfg, client := setupRefServiceWithoutRepo(t)

	tests := []struct {
		repo *gitalypb.Repository
		err  error
		desc string
	}{
		{
			desc: "nil repo",
			repo: nil,
			err:  status.Error(codes.InvalidArgument, gitalyOrPraefect("empty Repository", "repo scoped: empty Repository")),
		},
		{
			desc: "invalid storage name",
			repo: &gitalypb.Repository{StorageName: "foo"},
			err:  status.Error(codes.InvalidArgument, gitalyOrPraefect(`GetStorageByName: no such storage: "foo"`, "repo scoped: invalid Repository")),
		},
		{
			desc: "non-existing repo",
			repo: &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "bar"},
			err: status.Error(
				codes.NotFound,
				gitalyOrPraefect(
					fmt.Sprintf(`GetRepoPath: not a git repository: "%s/bar"`, cfg.Storages[0].Path),
					`mutator call: route repository mutator: get repository id: repository "default"/"bar" not found`,
				),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			//nolint:staticcheck
			_, err := client.PackRefs(ctx, &gitalypb.PackRefsRequest{Repository: tc.repo})
			testhelper.RequireGrpcError(t, err, tc.err)
		})
	}
}
