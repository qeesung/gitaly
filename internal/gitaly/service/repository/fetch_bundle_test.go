//go:build !gitaly_test_sha256

package repository

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
)

func TestServer_FetchBundle_success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, _, repoPath, client := setupRepositoryService(ctx, t)

	tmp := testhelper.TempDir(t)
	bundlePath := filepath.Join(tmp, "test.bundle")

	gittest.Exec(t, cfg, "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/feature")
	gittest.Exec(t, cfg, "-C", repoPath, "bundle", "create", bundlePath, "--all")
	expectedRefs := gittest.Exec(t, cfg, "-C", repoPath, "show-ref", "--head")

	targetRepo, targetRepoPath := gittest.CreateRepository(ctx, t, cfg)

	stream, err := client.FetchBundle(ctx)
	require.NoError(t, err)

	request := &gitalypb.FetchBundleRequest{Repository: targetRepo, UpdateHead: true}
	writer := streamio.NewWriter(func(p []byte) error {
		request.Data = p

		if err := stream.Send(request); err != nil {
			return err
		}

		request = &gitalypb.FetchBundleRequest{}

		return nil
	})

	bundle, err := os.Open(bundlePath)
	require.NoError(t, err)
	defer testhelper.MustClose(t, bundle)

	_, err = io.Copy(writer, bundle)
	require.NoError(t, err)

	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	refs := gittest.Exec(t, cfg, "-C", targetRepoPath, "show-ref", "--head")
	require.Equal(t, string(expectedRefs), string(refs))
}

func TestServer_FetchBundle_transaction(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	hookManager := &mockHookManager{}
	client, _ := runRepositoryService(t, cfg, nil, testserver.WithHookManager(hookManager), testserver.WithDisablePraefect())

	tmp := testhelper.TempDir(t)
	bundlePath := filepath.Join(tmp, "test.bundle")
	gittest.BundleRepo(t, cfg, repoPath, bundlePath)

	hookManager.Reset()
	_, stopGitServer := gittest.HTTPServer(ctx, t, gitCmdFactory, repoPath, nil)
	defer func() { require.NoError(t, stopGitServer()) }()

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	require.Empty(t, hookManager.states)

	stream, err := client.FetchBundle(ctx)
	require.NoError(t, err)

	request := &gitalypb.FetchBundleRequest{Repository: repoProto}
	writer := streamio.NewWriter(func(p []byte) error {
		request.Data = p

		if err := stream.Send(request); err != nil {
			return err
		}

		request = &gitalypb.FetchBundleRequest{}

		return nil
	})

	bundle, err := os.Open(bundlePath)
	require.NoError(t, err)
	defer testhelper.MustClose(t, bundle)

	_, err = io.Copy(writer, bundle)
	require.NoError(t, err)

	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	require.Equal(t, []gitalyhook.ReferenceTransactionState{
		gitalyhook.ReferenceTransactionPrepared,
		gitalyhook.ReferenceTransactionCommitted,
	}, hookManager.states)
}

func TestServer_FetchBundle_validation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc                  string
		firstRequest          *gitalypb.FetchBundleRequest
		expectedStreamErr     string
		expectedStreamErrCode codes.Code
	}{
		{
			desc: "no repo",
			firstRequest: &gitalypb.FetchBundleRequest{
				Repository: nil,
			},
			expectedStreamErr:     "empty Repository",
			expectedStreamErrCode: codes.InvalidArgument,
		},
		{
			desc: "unknown repo",
			firstRequest: &gitalypb.FetchBundleRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "default",
					RelativePath: "unknown",
				},
			},
			expectedStreamErr: func() string {
				if testhelper.IsPraefectEnabled() {
					return `repository "default"/"unknown" not found`
				}

				return "not a git repository"
			}(),
			expectedStreamErrCode: codes.NotFound,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, client := setupRepositoryServiceWithoutRepo(t)
			ctx := testhelper.Context(t)

			stream, err := client.FetchBundle(ctx)
			require.NoError(t, err)

			err = stream.Send(tc.firstRequest)
			require.NoError(t, err)

			_, err = stream.CloseAndRecv()
			require.Error(t, err)
			if tc.expectedStreamErr != "" {
				require.Contains(t, err.Error(), tc.expectedStreamErr)
			}
			if tc.expectedStreamErrCode != 0 {
				require.Equal(t, tc.expectedStreamErrCode, helper.GrpcCode(err))
			}
		})
	}
}

type mockHookManager struct {
	gitalyhook.Manager
	states []gitalyhook.ReferenceTransactionState
}

func (m *mockHookManager) Reset() {
	m.states = make([]gitalyhook.ReferenceTransactionState, 0)
}

func (m *mockHookManager) ReferenceTransactionHook(_ context.Context, state gitalyhook.ReferenceTransactionState, _ []string, _ io.Reader) error {
	m.states = append(m.states, state)
	return nil
}
