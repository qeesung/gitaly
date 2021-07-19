package teststream

import (
	"bytes"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	gitalyServer "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v14/internal/streamrpc"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestTestStreamPingPong(t *testing.T) {
	const size = 1024 * 1024

	addr, repo := runGitalyServer(t)

	ctx, cancel := testhelper.Context()
	defer cancel()

	in := make([]byte, size)
	_, err := rand.Read(in)
	require.NoError(t, err)

	var out []byte
	require.NotEqual(t, in, out)
	require.NoError(t, streamrpc.Call(
		ctx,
		streamrpc.DialNet(addr),
		"/gitaly.TestStreamService/TestStream",
		&gitalypb.TestStreamRequest{
			Repository: repo,
			Size:       size,
		},
		func(c net.Conn) error {
			errC := make(chan error, 1)
			go func() {
				var err error
				out, err = ioutil.ReadAll(c)
				errC <- err
			}()

			if _, err := io.Copy(c, bytes.NewReader(in)); err != nil {
				return err
			}
			if err := <-errC; err != nil {
				return err
			}

			return nil
		},
	))

	require.Equal(t, in, out, "byte stream works")
}

func TestTestStreamPingPongWithInvalidRepo(t *testing.T) {
	addr, repo := runGitalyServer(t)

	ctx, cancel := testhelper.Context()
	defer cancel()

	err := streamrpc.Call(
		ctx,
		streamrpc.DialNet(addr),
		"/gitaly.TestStreamService/TestStream",
		&gitalypb.TestStreamRequest{
			Repository: &gitalypb.Repository{
				StorageName:   repo.StorageName,
				RelativePath:  "@hashed/94/00/notexist.git",
				GlRepository:  repo.GlRepository,
				GlProjectPath: repo.GlProjectPath,
			},
			Size: 1024 * 1024,
		},
		func(c net.Conn) error {
			panic("Should not reach here")
		},
	)

	require.Error(t, err)
	require.Contains(
		t, err.Error(),
		"rpc error: code = NotFound desc = GetRepoPath: not a git repository",
	)
}

func runGitalyServer(t *testing.T) (string, *gitalypb.Repository) {
	t.Helper()
	testhelper.Configure()

	cfg, repo, _ := testcfg.BuildWithRepo(t)

	sf := createServerFactory(t, cfg)
	addr := "localhost:0"
	secure := false

	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })

	srv, err := sf.CreateExternal(secure)
	for _, streamRPCServer := range sf.StreamRPCServers {
		gitalypb.RegisterTestStreamServiceServer(streamRPCServer, NewServer(config.NewLocator(cfg)))
	}

	require.NoError(t, err)

	go srv.Serve(listener)

	return "tcp://" + listener.Addr().String(), repo
}

func createServerFactory(t *testing.T, cfg config.Cfg) *gitalyServer.GitalyServerFactory {
	registry := backchannel.NewRegistry()
	locator := config.NewLocator(cfg)
	cache := cache.New(cfg, locator)

	return gitalyServer.NewGitalyServerFactory(cfg, testhelper.DiscardTestEntry(t), registry, cache)
}
