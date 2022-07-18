//go:build !gitaly_test_sha256

package ps

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

var pid = os.Getpid()

func TestFailure(t *testing.T) {
	_, err := Exec(pid, "not-existing-keyword=")
	require.Error(t, err)
}

func TestComm(t *testing.T) {
	comm, err := Comm(pid)
	require.NoError(t, err)
	// the name of the testing binary may vary depending on how test are invoked (make or IDE)
	require.Contains(t, comm, "test")
}

func TestRSS(t *testing.T) {
	rss, err := RSS(pid)
	require.NoError(t, err)
	require.True(t, rss > 0, "Expected a positive RSS")
}
