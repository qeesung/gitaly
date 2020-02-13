package repository

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestGetConnectionByStorage(t *testing.T) {
	s := server{connsByAddress: make(map[string]*grpc.ClientConn)}

	ctx, cancel := testhelper.Context()
	defer cancel()

	storageName, address := "default", "unix://fake/address/wont/work"
	injectedCtx, err := helper.InjectGitalyServers(ctx, storageName, address, "token")
	require.NoError(t, err)

	md, ok := metadata.FromOutgoingContext(injectedCtx)
	require.True(t, ok)

	incomingCtx := metadata.NewIncomingContext(ctx, md)

	cc, err := s.getConnectionByStorage(incomingCtx, storageName)
	require.NoError(t, err)

	cc1, err := s.getConnectionByStorage(incomingCtx, storageName)
	require.NoError(t, err)
	require.True(t, cc == cc1, "cc1 should be the cached copy")
}

func TestGetConnectionsConcurrentAccess(t *testing.T) {
	s := server{connsByAddress: make(map[string]*grpc.ClientConn)}

	address := "unix://fake/address/wont/work"

	var remoteClient gitalypb.RemoteServiceClient
	var cc *grpc.ClientConn

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		var err error
		cc, err = s.getOrCreateConnection(address, "")
		require.NoError(t, err)
		wg.Done()
	}()

	go func() {
		var err error
		remoteClient, err = s.newRemoteClient()
		require.NoError(t, err)
		wg.Done()
	}()

	wg.Wait()
	require.NotNil(t, cc)
	require.NotNil(t, remoteClient)
}
