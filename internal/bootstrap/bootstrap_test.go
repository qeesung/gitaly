package bootstrap

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"syscall"
	"testing"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/config"

	"github.com/stretchr/testify/require"
)

var testConfigGracefulRestartTimeout = 2 * time.Second

type mockUpgrader struct {
	exit      chan struct{}
	hasParent bool
}

func (m *mockUpgrader) Exit() <-chan struct{} {
	return m.exit
}

func (m *mockUpgrader) HasParent() bool {
	return m.hasParent
}

func (m *mockUpgrader) Ready() error { return nil }

func (m *mockUpgrader) Upgrade() error {
	// to upgrade we close the exit channel
	close(m.exit)
	return nil
}

type testServer struct {
	server    *http.Server
	listeners map[string]net.Listener
	url       string
}

func (s *testServer) slowRequest(duration time.Duration) <-chan error {
	done := make(chan error)

	go func() {
		r, err := http.Get(fmt.Sprintf("%sslow?seconds=%d", s.url, int(duration.Seconds())))
		if r != nil {
			r.Body.Close()
		}

		done <- err
	}()

	return done
}

func TestCreateUnixListener(t *testing.T) {
	socketPath := path.Join(os.TempDir(), "gitaly-test-unix-socket")
	if err := os.Remove(socketPath); err != nil {
		require.True(t, os.IsNotExist(err), "cannot delete dangling socket: %v", err)
	}

	// simulate a dangling socket
	require.NoError(t, ioutil.WriteFile(socketPath, nil, 0755))

	listen := func(network, addr string) (net.Listener, error) {
		require.Equal(t, "unix", network)
		require.Equal(t, socketPath, addr)

		return net.Listen(network, addr)
	}
	u := &mockUpgrader{}
	b, err := _new(u, listen, false)
	require.NoError(t, err)

	// first boot
	l, err := b.listen("unix", socketPath)
	require.NoError(t, err, "failed to bind on first boot")
	require.NoError(t, l.Close())

	// simulate binding during an upgrade
	u.hasParent = true
	l, err = b.listen("unix", socketPath)
	require.NoError(t, err, "failed to bind on upgrade")
	require.NoError(t, l.Close())
}

func testWaitDuration(t *testing.T, b *Bootstrap, timeout time.Duration) error {
	waitCh := make(chan error)
	go func() { waitCh <- b.Wait() }()

	select {
	case <-time.After(timeout):
		t.Fatal("time out waiting for b.Wait()")
	case waitErr := <-waitCh:
		return waitErr
	}

	return nil
}

func TestImmediateTerminationOnSocketError(t *testing.T) {
	b, server := makeBootstrap(t)

	require.NoError(t, server.listeners["tcp"].Close(), "Closing first listener")

	err := testWaitDuration(t, b, 1*time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "use of closed network connection")
}

func TestImmediateTerminationOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			b, server := makeBootstrap(t)

			done := server.slowRequest(3 * time.Minute)

			time.AfterFunc(500*time.Millisecond, func() {
				self, err := os.FindProcess(os.Getpid())
				require.NoError(t, err)

				require.NoError(t, self.Signal(sig))
			})

			err := testWaitDuration(t, b, 1*time.Second)
			require.Error(t, err)
			require.Contains(t, err.Error(), "received signal")
			require.Contains(t, err.Error(), sig.String())

			server.server.Close()

			require.Error(t, <-done)
		})
	}
}

func TestGracefulTerminationStuck(t *testing.T) {
	b, server := makeBootstrap(t)

	require.Contains(t, testGracefulUpdate(t, server, b, testConfigGracefulRestartTimeout+(1*time.Second)).Error(), "grace period expired")
}

func TestGracefulTerminationWithSignals(t *testing.T) {
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)

	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			b, server := makeBootstrap(t)

			time.AfterFunc(500*time.Millisecond, func() {
				require.NoError(t, self.Signal(sig))
			})

			require.Contains(t, testGracefulUpdate(t, server, b, 1*time.Second).Error(), "force shutdown")
		})
	}
}

func TestGracefulTerminationServerErrors(t *testing.T) {
	b, server := makeBootstrap(t)

	done := make(chan error, 1)
	// This is a simulation of receiving a listener error during waitGracePeriod
	b.StopAction = func() {
		// we close the unix listener in order to test that the shutdown will not fail, but it keep waiting for the TCP request
		require.NoError(t, server.listeners["unix"].Close())

		// we start a new TCP request that if faster than the grace period
		req := server.slowRequest(config.Config.GracefulRestartTimeout / 2)
		done <- <-req
		close(done)

		server.server.Shutdown(context.Background())
	}

	require.Contains(t, testGracefulUpdate(t, server, b, testConfigGracefulRestartTimeout+(1*time.Second)).Error(), "grace period expired")

	require.NoError(t, <-done)
}

func TestGracefulTermination(t *testing.T) {
	b, server := makeBootstrap(t)

	// Using server.Close we bypass the graceful shutdown faking a completed shutdown
	b.StopAction = func() { server.server.Close() }

	require.Contains(t, testGracefulUpdate(t, server, b, 1*time.Second).Error(), "completed")
}

func testGracefulUpdate(t *testing.T, server *testServer, b *Bootstrap, waitTimeout time.Duration) error {
	defer func(oldVal time.Duration) {
		config.Config.GracefulRestartTimeout = oldVal
	}(config.Config.GracefulRestartTimeout)
	config.Config.GracefulRestartTimeout = testConfigGracefulRestartTimeout

	// Start a slow request to keep the old server from shutting down immediately.
	req := server.slowRequest(2 * config.Config.GracefulRestartTimeout)

	// Simulate an upgrade request after entering into the blocking b.Wait() and during the slowRequest execution
	time.AfterFunc(300*time.Millisecond, func() {
		b.upgrader.Upgrade()
	})

	waitErr := testWaitDuration(t, b, waitTimeout)
	require.Error(t, waitErr)
	require.Contains(t, waitErr.Error(), "graceful upgrade")

	server.server.Close()

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for client error")
	case clientErr := <-req:
		require.Error(t, clientErr, "slow request not terminated after the grace period")
	}

	return waitErr
}

func makeBootstrap(t *testing.T) (*Bootstrap, *testServer) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		sec, err := strconv.Atoi(r.URL.Query().Get("seconds"))
		require.NoError(t, err)

		t.Logf("Serving a slow request for %d seconds", sec)
		time.Sleep(time.Duration(sec) * time.Second)

		w.WriteHeader(200)
	})

	s := http.Server{Handler: mux}
	u := &mockUpgrader{exit: make(chan struct{})}

	b, err := _new(u, net.Listen, false)
	require.NoError(t, err)

	b.StopAction = func() { s.Shutdown(context.Background()) }

	listeners := make(map[string]net.Listener)
	start := func(network, address string) Starter {
		return func(listen ListenFunc, errors chan<- error) error {
			l, err := listen(network, address)
			if err != nil {
				return err
			}
			listeners[network] = l

			go func() {
				errors <- s.Serve(l)
			}()

			return nil
		}
	}

	for network, address := range map[string]string{
		"tcp":  "127.0.0.1:0",
		"unix": path.Join(os.TempDir(), "gitaly-test-unix-socket"),
	} {
		b.RegisterStarter(start(network, address))
	}

	require.NoError(t, b.Start())
	require.Equal(t, 2, len(listeners))

	// test connection
	addr := listeners["tcp"].Addr()
	url := fmt.Sprintf("http://%s/", addr.String())

	r, err := http.Get(url)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, 200, r.StatusCode)

	return b, &testServer{
		server:    &s,
		listeners: listeners,
		url:       url,
	}
}
