//go:build !gitaly_test_sha256

package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"google.golang.org/grpc/metadata"
)

func TestGetConnectionByStorage(t *testing.T) {
	t.Parallel()
	connPool := client.NewPool()
	defer connPool.Close()

	s := server{conns: connPool}
	ctx := testhelper.Context(t)

	storageName, address := "default", "unix:///fake/address/wont/work"
	injectedCtx, err := storage.InjectGitalyServers(ctx, storageName, address, "token")
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(injectedCtx)
	require.True(t, ok)

	incomingCtx := metadata.NewIncomingContext(ctx, md)

	_, err = s.newRepoClient(incomingCtx, storageName)
	require.NoError(t, err)
}
