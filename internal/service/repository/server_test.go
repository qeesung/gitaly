package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/connection"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"google.golang.org/grpc/metadata"
)

func TestGetConnectionByStorage(t *testing.T) {
	connPool := connection.NewPool()
	defer connPool.Close()

	s := server{conns: connPool}

	ctx, cancel := testhelper.Context()
	defer cancel()

	storageName, address := "default", "unix://fake/address/wont/work"
	injectedCtx, err := helper.InjectGitalyServers(ctx, storageName, address, "token")
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(injectedCtx)
	require.True(t, ok)

	incomingCtx := metadata.NewIncomingContext(ctx, md)

	_, err = s.newRepoClient(incomingCtx, storageName)
	require.NoError(t, err)
}
