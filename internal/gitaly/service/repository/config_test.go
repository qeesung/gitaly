package repository

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc/codes"
)

func TestGetConfig(t *testing.T) {
	t.Parallel()
	cfg, client := setupRepositoryService(t)

	ctx := testhelper.Context(t)

	getConfig := func(
		t *testing.T,
		client gitalypb.RepositoryServiceClient,
		repo *gitalypb.Repository,
	) (string, error) {
		stream, err := client.GetConfig(ctx, &gitalypb.GetConfigRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		reader := streamio.NewReader(func() ([]byte, error) {
			response, err := stream.Recv()
			var bytes []byte
			if response != nil {
				bytes = response.Data
			}
			return bytes, err
		})

		contents, err := io.ReadAll(reader)
		return string(contents), err
	}

	t.Run("normal repo", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(t, ctx, cfg)

		config, err := getConfig(t, client, repo)
		require.NoError(t, err)

		var darwinConfig string
		if runtime.GOOS == "darwin" {
			darwinConfig = "\tignorecase = true\n\tprecomposeunicode = true\n"
		}

		var extensionsForSha1 string
		extensionsForSha256 := "[extensions]\n\tobjectformat = sha256\n"
		repoFormatVersion := 0

		if testhelper.IsReftableEnabled() {
			repoFormatVersion = 1
			extensionsForSha1 = "[extensions]\n\trefstorage = reftable\n"
			extensionsForSha256 += "\trefstorage = reftable\n"
		}

		expectedConfig := gittest.ObjectHashDependent(t, map[string]string{
			"sha1": fmt.Sprintf(
				"[core]\n\trepositoryformatversion = %d\n\tfilemode = true\n\tbare = true\n%s%s",
				repoFormatVersion,
				darwinConfig,
				extensionsForSha1,
			),
			"sha256": fmt.Sprintf(
				"[core]\n\trepositoryformatversion = 1\n\tfilemode = true\n\tbare = true\n%s%s",
				darwinConfig,
				extensionsForSha256,
			),
		})

		require.Equal(t, expectedConfig, config)
	})

	t.Run("missing config", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		configPath := filepath.Join(repoPath, "config")
		require.NoError(t, os.Remove(configPath))

		config, err := getConfig(t, client, repo)
		testhelper.RequireGrpcCode(t, err, codes.NotFound)
		require.Regexp(t, "^rpc error: code = NotFound desc = opening gitconfig: open .+/config: no such file or directory$", err.Error())
		require.Equal(t, "", config)
	})

	t.Run("no repository provided", func(t *testing.T) {
		_, err := getConfig(t, client, nil)
		testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
	})
}
