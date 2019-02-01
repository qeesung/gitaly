package gitalox_test

import (
	"context"
	"net"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/gitalox"
)

func TestServerRouting(t *testing.T) {
	loxSrv := gitalox.NewServer(nil)

	listener, port := listenAvailPort(t)
	t.Logf("proxy listening on port %d", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errQ := make(chan error)

	go func() {
		errQ <- loxSrv.Start(ctx, listener)
	}()

	cancel()
	t.Logf("Server teminated: %s", <-errQ)
}

func listenAvailPort(tb testing.TB) (net.Listener, int) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		tb.Fatalf("unable to listen on available TCP port: %q", err)
	}

	return listener, listener.Addr().(*net.TCPAddr).Port
}
