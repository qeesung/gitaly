package server

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
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	gitalyServer "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v14/internal/streamrpc"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

func TestTestStreamPingPong(t *testing.T) {
	size := int64(1024 * 1024)

	addr := runGitalyServer(t)

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
		"/gitaly.ServerService/TestStream",
		&gitalypb.TestStreamRequest{Size: size},
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

			return c.Close()
		},
	))

	require.Equal(t, in, out, "byte stream works")
}

func runGitalyServer(t *testing.T) string {
	t.Helper()
	testhelper.Configure()

	cfg := testcfg.Build(t)

	sf := createServerFactory(t, cfg)
	addr := "localhost:0"
	secure := false

	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })

	srv, err := sf.CreateExternal(secure)
	require.NoError(t, err)

	go srv.Serve(listener)

	return "tcp://" + listener.Addr().String()
}

func createServerFactory(t *testing.T, cfg config.Cfg) *gitalyServer.GitalyServerFactory {
	registry := backchannel.NewRegistry()
	cache := cache.New(cfg, config.NewLocator(cfg))
	streamRPCServer := streamrpc.NewServer()

	gitalypb.RegisterServerServiceServer(streamRPCServer, NewServer(
		git.NewExecCommandFactory(cfg), cfg.Storages,
	))
	return gitalyServer.NewGitalyServerFactory(cfg, testhelper.DiscardTestEntry(t), registry, cache, streamRPCServer)
}
