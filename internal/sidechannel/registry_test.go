//go:build !gitaly_test_sha256

package sidechannel

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestRegistry(t *testing.T) {
	const N = 10
	registry := NewRegistry()

	t.Run("waiter removed from the registry right after connection received", func(t *testing.T) {
		triggerCallback := make(chan struct{})
		waiter := registry.Register(func(conn *ClientConn) error {
			<-triggerCallback
			return nil
		})
		defer testhelper.MustClose(t, waiter)

		require.Equal(t, 1, registry.waiting())

		client, _ := socketPair(t)
		require.NoError(t, registry.receive(waiter.id, client))
		require.Equal(t, 0, registry.waiting())

		close(triggerCallback)

		require.NoError(t, waiter.Close())
		requireConnClosed(t, client)
	})

	t.Run("receive connections successfully", func(t *testing.T) {
		wg := sync.WaitGroup{}
		servers := make([]net.Conn, N)

		for i := 0; i < N; i++ {
			client, server := socketPair(t)
			servers[i] = server
			defer server.Close()

			wg.Add(1)
			go func(i int) {
				waiter := registry.Register(func(conn *ClientConn) error {
					if _, err := fmt.Fprintf(conn, "%d", i); err != nil {
						return err
					}

					return conn.CloseWrite()
				})
				defer testhelper.MustClose(t, waiter)

				require.NoError(t, registry.receive(waiter.id, client))
				require.NoError(t, waiter.Close())
				requireConnClosed(t, client)

				wg.Done()
			}(i)
		}

		for i := 0; i < N; i++ {
			// Read registry confirmation
			buf := make([]byte, 2)
			_, err := io.ReadFull(servers[i], buf)
			require.NoError(t, err)
			require.Equal(t, "ok", string(buf))

			// Read data written by callback
			out, err := io.ReadAll(newServerConn(servers[i]))
			require.NoError(t, err)
			require.Equal(t, strconv.Itoa(i), string(out))
		}

		wg.Wait()
		require.Equal(t, 0, registry.waiting())
	})

	t.Run("receive connection for non-existing ID", func(t *testing.T) {
		client, _ := socketPair(t)
		err := registry.receive(registry.nextID+1, client)
		require.EqualError(t, err, "sidechannel registry: ID not registered")
		requireConnClosed(t, client)
	})

	t.Run("pre-maturely close the waiter", func(t *testing.T) {
		waiter := registry.Register(func(conn *ClientConn) error { panic("never execute") })
		require.Equal(t, ErrCallbackDidNotRun, waiter.Close())
		require.Equal(t, 0, registry.waiting())
	})
}

func requireConnClosed(t *testing.T, conn net.Conn) {
	one := make([]byte, 1)
	_, err := conn.Read(one)
	require.Errorf(t, err, "use of closed network connection")
	_, err = conn.Write(one)
	require.Errorf(t, err, "use of closed network connection")
}

func socketPair(t *testing.T) (net.Conn, net.Conn) {
	conns := make([]net.Conn, 2)
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)

	for i, fd := range fds[:] {
		f := os.NewFile(uintptr(fd), "socket pair")
		c, err := net.FileConn(f)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		t.Cleanup(func() { c.Close() })
		conns[i] = c
	}
	return conns[0], conns[1]
}
