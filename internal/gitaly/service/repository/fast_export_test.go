package repository

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestFastExport(t *testing.T) {
	t.Parallel()

	cfg, client := setupRepositoryService(t)
	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch-a"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{
				Mode:    "100644",
				Path:    "README.md",
				Content: "readme content",
			},
		),
	)

	t.Run("convert repo to sha 256", func(t *testing.T) {
		// NOTE: when running tests with sha256 object format as the default, this
		// test is essentially a noop
		stream, err := client.FastExport(ctx, &gitalypb.FastExportRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		r := consumeFastExportResponse(t, stream)

		_, repoCopyPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			ObjectFormat: "sha256",
		})

		gittest.ExecOpts(t, cfg, gittest.ExecConfig{
			Stdin: r,
		}, "-C", repoCopyPath, "fast-import")

		gittest.RequireTree(t, cfg, repoCopyPath, "branch-a",
			[]gittest.TreeEntry{
				{
					Mode:    "100644",
					Path:    "README.md",
					Content: "readme content",
				},
			}, gittest.WithSha256())
	})
}

func consumeFastExportResponse(
	t *testing.T,
	stream gitalypb.RepositoryService_FastExportClient,
) io.Reader {
	r, w := io.Pipe()

	go func() {
		defer w.Close()
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				_, err = w.Write(resp.GetData())
				require.NoError(t, err)
				break
			}
			require.NoError(t, err)

			_, err = w.Write(resp.GetData())
			require.NoError(t, err)
		}
	}()

	return r
}
