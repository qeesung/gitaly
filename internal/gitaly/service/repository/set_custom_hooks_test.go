package repository

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc/codes"
)

func TestSetCustomHooksRequest_success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc         string
		streamWriter func(*testing.T, context.Context, *gitalypb.Repository, gitalypb.RepositoryServiceClient) (io.Writer, func())
	}{
		{
			desc: "SetCustomHooks",
			streamWriter: func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, client gitalypb.RepositoryServiceClient) (io.Writer, func()) {
				stream, err := client.SetCustomHooks(ctx)
				require.NoError(t, err)

				request := &gitalypb.SetCustomHooksRequest{Repository: repo}
				writer := streamio.NewWriter(func(p []byte) error {
					request.Data = p
					if err := stream.Send(request); err != nil {
						return err
					}

					request = &gitalypb.SetCustomHooksRequest{}
					return nil
				})

				closeFunc := func() {
					_, err = stream.CloseAndRecv()
					require.NoError(t, err)
				}

				return writer, closeFunc
			},
		},
		{
			desc: "RestoreCustomHooks",
			streamWriter: func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, client gitalypb.RepositoryServiceClient) (io.Writer, func()) {
				testhelper.SkipWithWAL(t, "This RPC is deprecated and pending removal")

				//nolint:staticcheck
				stream, err := client.RestoreCustomHooks(ctx)
				require.NoError(t, err)

				request := &gitalypb.RestoreCustomHooksRequest{Repository: repo}
				writer := streamio.NewWriter(func(p []byte) error {
					request.Data = p
					if err := stream.Send(request); err != nil {
						return err
					}

					request = &gitalypb.RestoreCustomHooksRequest{}
					return nil
				})

				closeFunc := func() {
					_, err = stream.CloseAndRecv()
					require.NoError(t, err)
				}

				return writer, closeFunc
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := testcfg.Build(t)
			testcfg.BuildGitalyHooks(t, cfg)
			txManager := transaction.NewTrackingManager()

			client, addr := runRepositoryService(t, cfg, testserver.WithTransactionManager(txManager))
			cfg.SocketPath = addr

			ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
			require.NoError(t, err)

			ctx = metadata.IncomingToOutgoing(ctx)
			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

			// reset the txManager since CreateRepository would have done
			// voting
			txManager.Reset()

			writer, closeStream := tc.streamWriter(t, ctx, repo, client)

			archivePath := mustCreateCustomHooksArchive(t, ctx, []testFile{
				{name: "pre-commit.sample", content: "foo", mode: 0o755},
				{name: "pre-push.sample", content: "bar", mode: 0o755},
			}, repoutil.CustomHooksDir)

			file, err := os.Open(archivePath)
			require.NoError(t, err)

			_, err = io.Copy(writer, file)
			require.NoError(t, err)
			closeStream()

			testhelper.MustClose(t, file)

			require.FileExists(t, filepath.Join(repoPath, "custom_hooks", "pre-push.sample"))
			require.Equal(t, 2, len(txManager.Votes()))
			assert.Equal(t, voting.Prepared, txManager.Votes()[0].Phase)
			assert.Equal(t, voting.Committed, txManager.Votes()[1].Phase)
		})
	}
}

func TestSetCustomHooks_failedValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc         string
		streamSender func(*testing.T, context.Context, gitalypb.RepositoryServiceClient) error
	}{
		{
			desc: "SetCustomHooks",
			streamSender: func(t *testing.T, ctx context.Context, client gitalypb.RepositoryServiceClient) error {
				stream, err := client.SetCustomHooks(ctx)
				require.NoError(t, err)

				require.NoError(t, stream.Send(&gitalypb.SetCustomHooksRequest{}))

				_, err = stream.CloseAndRecv()
				return err
			},
		},
		{
			desc: "RestoreCustomHooks",
			streamSender: func(t *testing.T, ctx context.Context, client gitalypb.RepositoryServiceClient) error {
				//nolint:staticcheck
				stream, err := client.RestoreCustomHooks(ctx)
				require.NoError(t, err)

				require.NoError(t, stream.Send(&gitalypb.RestoreCustomHooksRequest{}))

				_, err = stream.CloseAndRecv()
				return err
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, client := setupRepositoryService(t)

			err := tc.streamSender(t, ctx, client)
			testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
		})
	}
}

func TestSetCustomHooks_corruptTar(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc         string
		streamWriter func(*testing.T, context.Context, *gitalypb.Repository, gitalypb.RepositoryServiceClient) (io.Writer, func() error)
	}{
		{
			desc: "SetCustomHooks",
			streamWriter: func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, client gitalypb.RepositoryServiceClient) (io.Writer, func() error) {
				stream, err := client.SetCustomHooks(ctx)
				require.NoError(t, err)

				request := &gitalypb.SetCustomHooksRequest{Repository: repo}
				writer := streamio.NewWriter(func(p []byte) error {
					request.Data = p
					if err := stream.Send(request); err != nil {
						return err
					}

					request = &gitalypb.SetCustomHooksRequest{}
					return nil
				})

				closeFunc := func() error {
					_, err = stream.CloseAndRecv()
					return err
				}

				return writer, closeFunc
			},
		},
		{
			desc: "RestoreCustomHooks",
			streamWriter: func(t *testing.T, ctx context.Context, repo *gitalypb.Repository, client gitalypb.RepositoryServiceClient) (io.Writer, func() error) {
				//nolint:staticcheck
				stream, err := client.RestoreCustomHooks(ctx)
				require.NoError(t, err)

				request := &gitalypb.RestoreCustomHooksRequest{Repository: repo}
				writer := streamio.NewWriter(func(p []byte) error {
					request.Data = p
					if err := stream.Send(request); err != nil {
						return err
					}

					request = &gitalypb.RestoreCustomHooksRequest{}
					return nil
				})

				closeFunc := func() error {
					_, err = stream.CloseAndRecv()
					return err
				}

				return writer, closeFunc
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, client := setupRepositoryService(t)
			repo, _ := gittest.CreateRepository(t, ctx, cfg)
			writer, closeStream := tc.streamWriter(t, ctx, repo, client)

			archivePath := mustCreateCorruptHooksArchive(t)

			file, err := os.Open(archivePath)
			require.NoError(t, err)
			defer testhelper.MustClose(t, file)

			_, err = io.Copy(writer, file)
			require.NoError(t, err)
			err = closeStream()
			testhelper.RequireGrpcCode(t, err, codes.Internal)
		})
	}
}

type testFile struct {
	name    string
	content string
	mode    os.FileMode
}

func mustWriteCustomHookDirectory(t *testing.T, files []testFile, dirName string) string {
	t.Helper()

	tmpDir := testhelper.TempDir(t)
	hooksPath := filepath.Join(tmpDir, dirName)

	err := os.Mkdir(hooksPath, perm.PrivateDir)
	require.NoError(t, err)

	for _, f := range files {
		err = os.WriteFile(filepath.Join(hooksPath, f.name), []byte(f.content), f.mode)
		require.NoError(t, err)
	}

	return hooksPath
}

func mustCreateCustomHooksArchive(t *testing.T, ctx context.Context, files []testFile, dirName string) string {
	t.Helper()

	hooksPath := mustWriteCustomHookDirectory(t, files, dirName)
	hooksDir := filepath.Dir(hooksPath)

	tmpDir := testhelper.TempDir(t)
	archivePath := filepath.Join(tmpDir, "custom_hooks.tar")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	err = archive.WriteTarball(ctx, testhelper.NewLogger(t), file, hooksDir, dirName)
	require.NoError(t, err)

	return archivePath
}

func mustCreateCorruptHooksArchive(t *testing.T) string {
	t.Helper()

	tmpDir := testhelper.TempDir(t)
	archivePath := filepath.Join(tmpDir, "corrupt_hooks.tar")

	err := os.WriteFile(archivePath, []byte("This is a corrupted tar file"), 0o755)
	require.NoError(t, err)

	return archivePath
}
