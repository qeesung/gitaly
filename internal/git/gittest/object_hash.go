package gittest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
)

// DefaultObjectHash is the default object hash used for running tests.
var DefaultObjectHash = func() git.ObjectHash {
	if _, enabled := os.LookupEnv("GITALY_TEST_WITH_SHA256"); enabled {
		return git.ObjectHashSHA256
	}

	return git.ObjectHashSHA1
}()

// SkipWithSHA256 skips the test in case the default object hash is SHA256.
func SkipWithSHA256(tb testing.TB) {
	tb.Helper()

	if DefaultObjectHash.Format == "sha256" {
		tb.Skip("test is not compatible with SHA256")
	}
}

// ObjectHashIsSHA256 returns if the current default object hash is SHA256.
func ObjectHashIsSHA256() bool {
	return DefaultObjectHash.EmptyTreeOID == git.ObjectHashSHA256.EmptyTreeOID
}

// ObjectHashDependent returns the value from the given map that is associated with the default
// object hash (e.g. "sha1", "sha256"). Fails in case the map doesn't contain the current object
// hash.
func ObjectHashDependent[T any](tb testing.TB, valuesByObjectHash map[string]T) T {
	tb.Helper()
	require.Contains(tb, valuesByObjectHash, DefaultObjectHash.Format)
	return valuesByObjectHash[DefaultObjectHash.Format]
}
