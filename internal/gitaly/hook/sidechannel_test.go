//go:build !gitaly_test_sha256

package hook

import (
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	grpc_metadata "google.golang.org/grpc/metadata"
)

func runTestsWithRuntimeDir(t *testing.T, testFunc func(*testing.T, string)) {
	t.Helper()

	t.Run("no runtime dir", func(t *testing.T) {
		testFunc(t, "")
	})

	t.Run("with runtime dir", func(t *testing.T) {
		testFunc(t, testhelper.TempDir(t))
	})
}

func TestSidechannel(t *testing.T) {
	t.Parallel()
	runTestsWithRuntimeDir(t, testSidechannelWithRuntimeDir)
}

func testSidechannelWithRuntimeDir(t *testing.T, runtimeDir string) {
	ctx := testhelper.Context(t)

	// Client side
	ctxOut, wt, err := SetupSidechannel(
		ctx,
		git.HooksPayload{
			RuntimeDir: runtimeDir,
		},
		func(c *net.UnixConn) error {
			_, err := io.WriteString(c, "ping")
			return err
		},
	)
	require.NoError(t, err)
	defer wt.Close()

	require.DirExists(t, wt.socketDir)

	// Server side
	ctxIn := metadata.OutgoingToIncoming(ctxOut)
	c, err := GetSidechannel(ctxIn)
	require.NoError(t, err)
	defer c.Close()

	buf, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))

	require.NoDirExists(t, wt.socketDir)

	// Client side
	require.NoError(t, wt.Wait())

	if runtimeDir != "" {
		require.DirExists(t, filepath.Join(runtimeDir, "chan.d"))
	}
}

func TestSidechannel_cleanup(t *testing.T) {
	t.Parallel()
	runTestsWithRuntimeDir(t, testSidechannelCleanupWithRuntimeDir)
}

func testSidechannelCleanupWithRuntimeDir(t *testing.T, runtimeDir string) {
	_, wt, err := SetupSidechannel(
		testhelper.Context(t),
		git.HooksPayload{
			RuntimeDir: runtimeDir,
		},
		func(c *net.UnixConn) error { return nil },
	)
	require.NoError(t, err)
	defer wt.Close()

	require.DirExists(t, wt.socketDir)
	_ = wt.Close()
	require.NoDirExists(t, wt.socketDir)

	if runtimeDir != "" {
		require.DirExists(t, filepath.Join(runtimeDir, "chan.d"))
	}
}

func TestGetSidechannel(t *testing.T) {
	ctx := testhelper.Context(t)

	testCases := []string{
		"foobar",
		"sc.foo/../../bar",
		"foo/../../bar",
		"/etc/passwd",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			ctx := grpc_metadata.NewIncomingContext(
				ctx,
				map[string][]string{sidechannelHeader: {tc}},
			)
			_, err := GetSidechannel(ctx)
			require.Error(t, err)
			require.Equal(t, &errInvalidSidechannelAddress{tc}, err)
		})
	}
}
