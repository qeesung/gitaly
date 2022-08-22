package testserver

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	gitalycfg "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

// praefectSpawnTokens limits the number of concurrent Praefect instances we spawn. With parallel
// tests, it can happen that we otherwise would spawn so many Praefect executables, with two
// consequences: first, they eat up all the hosts' memory. Second, they start to saturate Postgres
// such that new connections start to fail becaue of too many clients. The limit of concurrent
// instances is not scientifically chosen, but is picked such that tests do not fail on my machine
// anymore.
//
// Note that this only limits concurrency for a single package. If you test multiple packages at
// once, then these would also run concurrently, leading to `16 * len(packages)` concurrent Praefect
// instances. To limit this, you can run `go test -p $n` to test at most `$n` concurrent packages.
var praefectSpawnTokens = make(chan struct{}, 16)

// PraefectServer encapsulates information of a running Praefect server.
type PraefectServer struct {
	address  string
	shutdown func()
}

// Address is the address of the Praefect server.
func (ps PraefectServer) Address() string {
	return ps.address
}

// Shutdown shuts the Praefect server down. This function is synchronous and waits for the server to
// exit.
func (ps PraefectServer) Shutdown() {
	ps.shutdown()
}

// StartPraefect creates and runs a Praefect proxy. This server is created by running the external
// Praefect executable.
func StartPraefect(tb testing.TB, cfg config.Config) PraefectServer {
	tb.Helper()

	praefectSpawnTokens <- struct{}{}
	tb.Cleanup(func() {
		<-praefectSpawnTokens
	})

	// We're precreating the Unix socket which we pass to Praefect. This closes a race where
	// the Unix socket didn't yet exist when we tried to dial the Praefect server.
	praefectServerSocket, err := net.Listen("unix", cfg.SocketPath)
	require.NoError(tb, err)
	testhelper.MustClose(tb, praefectServerSocket)
	tb.Cleanup(func() { require.NoError(tb, os.RemoveAll(praefectServerSocket.Addr().String())) })

	tempDir := testhelper.TempDir(tb)

	configFilePath := filepath.Join(tempDir, "config.toml")
	configFile, err := os.Create(configFilePath)
	require.NoError(tb, err)
	defer testhelper.MustClose(tb, configFile)

	require.NoError(tb, toml.NewEncoder(configFile).Encode(&cfg))
	require.NoError(tb, configFile.Sync())

	binaryPath := testcfg.BuildPraefect(tb, gitalycfg.Cfg{
		BinDir: tempDir,
	})

	cmd := exec.Command(binaryPath, "-config", configFilePath)
	// Logs are written to stderr, we can ignore stdout.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout

	require.NoError(tb, cmd.Start())

	var waitErr error
	var waitOnce sync.Once
	wait := func() error {
		waitOnce.Do(func() {
			waitErr = cmd.Wait()
		})
		return waitErr
	}

	praefectServer := PraefectServer{
		address: "unix://" + praefectServerSocket.Addr().String(),
		shutdown: func() {
			_ = cmd.Process.Kill()
			_ = wait()
		},
	}
	tb.Cleanup(praefectServer.Shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	processExitedCh := make(chan error, 1)
	go func() {
		processExitedCh <- wait()
		cancel()
	}()

	// Ensure this runs even if context ends in waitHealthy.
	defer func() {
		// Check if the process has exited. This must not happen given that we need it to be
		// up in order to connect to it.
		select {
		case <-processExitedCh:
			require.FailNowf(tb, "Praefect has died", "%s", stderr.String())
		default:
		}

		select {
		case <-ctx.Done():
			switch ctx.Err() {
			case context.DeadlineExceeded:
				// Capture Praefect logs when waitHealthy takes too long.
				require.FailNowf(tb, "Connecting to Praefect exceeded deadline", "%s", stderr.String())
			}
		default:
		}
	}()

	waitHealthy(ctx, tb, praefectServer.Address(), cfg.Auth.Token)

	return praefectServer
}
