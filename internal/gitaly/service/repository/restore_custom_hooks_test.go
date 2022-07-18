//go:build !gitaly_test_sha256

package repository

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
)

func TestSuccessfulRestoreCustomHooksRequest(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionalRestoreCustomHooks).
		Run(t, testSuccessfulRestoreCustomHooksRequest)
}

func testSuccessfulRestoreCustomHooksRequest(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	txManager := transaction.NewTrackingManager()

	client, addr := runRepositoryService(t, cfg, nil, testserver.WithTransactionManager(txManager))
	cfg.SocketPath = addr

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)

	ctx = metadata.IncomingToOutgoing(ctx)
	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	// reset the txManager since CreateRepository would have done
	// voting
	txManager.Reset()
	stream, err := client.RestoreCustomHooks(ctx)
	require.NoError(t, err)

	request := &gitalypb.RestoreCustomHooksRequest{Repository: repo}
	voteHash := voting.NewVoteHash()

	writer := streamio.NewWriter(func(p []byte) error {
		voteHash.Write(p)
		request.Data = p
		if err := stream.Send(request); err != nil {
			return err
		}

		request = &gitalypb.RestoreCustomHooksRequest{}
		return nil
	})

	file, err := os.Open("testdata/custom_hooks.tar")
	require.NoError(t, err)

	_, err = io.Copy(writer, file)
	require.NoError(t, err)
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	testhelper.MustClose(t, file)

	expectedVote, err := voteHash.Vote()
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(repoPath, "custom_hooks", "pre-push.sample"))

	if featureflag.TransactionalRestoreCustomHooks.IsEnabled(ctx) {
		require.Equal(t, 2, len(txManager.Votes()))
		assert.Equal(t, voting.Prepared, txManager.Votes()[0].Phase)
		assert.Equal(t, expectedVote, txManager.Votes()[1].Vote)
		assert.Equal(t, voting.Committed, txManager.Votes()[1].Phase)
	} else {
		require.Equal(t, 0, len(txManager.Votes()))
	}
}

func TestFailedRestoreCustomHooksDueToValidations(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionalRestoreCustomHooks).
		Run(t, testFailedRestoreCustomHooksDueToValidations)
}

func testFailedRestoreCustomHooksDueToValidations(t *testing.T, ctx context.Context) {
	t.Parallel()
	_, client := setupRepositoryServiceWithoutRepo(t)

	stream, err := client.RestoreCustomHooks(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&gitalypb.RestoreCustomHooksRequest{}))

	_, err = stream.CloseAndRecv()
	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
}

func TestFailedRestoreCustomHooksDueToBadTar(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.TransactionalRestoreCustomHooks).
		Run(t, testFailedRestoreCustomHooksDueToBadTar)
}

func testFailedRestoreCustomHooksDueToBadTar(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRepositoryService(ctx, t)

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

	file, err := os.Open("testdata/corrupted_hooks.tar")
	require.NoError(t, err)
	defer file.Close()

	_, err = io.Copy(writer, file)
	require.NoError(t, err)
	_, err = stream.CloseAndRecv()

	testhelper.RequireGrpcCode(t, err, codes.Internal)
}
