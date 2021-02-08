package streamcache

import (
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestWriteOneReadMultiple(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	c, err := NewCache(tmp, time.Minute)
	require.NoError(t, err)
	defer c.Stop()

	const (
		key = "test key"
		N   = 10
	)
	content := func(i int) string { return fmt.Sprintf("content %d", i) }

	for i := 0; i < N; i++ {
		t.Run(fmt.Sprintf("read %d", i), func(t *testing.T) {
			r, created, err := c.FindOrCreate(key, writeString(content(i)))
			require.NoError(t, err)
			require.Equal(t, i == 0, created, "created?")

			out, err := ioutil.ReadAll(r)
			require.NoError(t, err)
			require.NoError(t, r.Close())
			require.Equal(t, content(0), string(out))
		})
	}

	requireCacheFiles(t, tmp, 1)
}

func writeString(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

func requireCacheFiles(t *testing.T, dir string, n int) {
	cachedFiles := strings.Split(
		text.ChompBytes(testhelper.MustRunCommand(t, nil, "find", dir, "-type", "f")),
		"\n",
	)

	found := 0
	for _, f := range cachedFiles {
		if f != "" {
			found++
		}
	}

	if found != n {
		t.Fatalf("expected %d files, got %v", n, cachedFiles)
	}
}

func requireCacheEntries(t *testing.T, c *Cache, n int) {
	c.m.Lock()
	defer c.m.Unlock()
	require.Len(t, c.index, n)
	require.Len(t, c.queue, n)
}

func TestCacheScope(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	const (
		N   = 100
		key = "test key"
	)

	// Intentionally create multiple cache instances sharing one directory,
	// to test that they do not trample on each others files.
	cache := make([]*Cache, N)
	input := make([]string, N)
	reader := make([]io.ReadCloser, N)
	var err error

	for i := 0; i < N; i++ {
		input[i] = fmt.Sprintf("test content %d", i)
		cache[i], err = NewCache(tmp, time.Minute)
		require.NoError(t, err)
		defer func(i int) { cache[i].Stop() }(i)

		var created bool
		reader[i], created, err = cache[i].FindOrCreate(key, writeString(input[i]))
		require.NoError(t, err)
		require.True(t, created, "created?")
	}

	// If different cache instances overwrite their entries, the effect may
	// be order dependent, e.g. "last write wins". By shuffling the order in
	// which we read back entries we should (eventually) catch such
	// collisions.
	rand.Shuffle(N, func(i, j int) {
		reader[i], reader[j] = reader[j], reader[i]
		input[i], input[j] = input[j], input[i]
	})

	for i := 0; i < N; i++ {
		r, content := reader[i], input[i]

		out, err := ioutil.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())

		require.Equal(t, content, string(out))
	}
}

func TestCacheDiskCleanup(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	const (
		key    = "test key"
		expiry = 10 * time.Millisecond
	)

	c, err := NewCache(tmp, expiry)
	require.NoError(t, err)
	defer c.Stop()

	content := func(i int) string { return fmt.Sprintf("content %d", i) }

	r1, created, err := c.FindOrCreate(key, writeString(content(1)))
	require.NoError(t, err)
	require.True(t, created, "created?")

	_, err = io.Copy(ioutil.Discard, r1)
	require.NoError(t, err)
	require.NoError(t, r1.Close())

	requireCacheFiles(t, tmp, 1)
	requireCacheEntries(t, c, 1)

	time.Sleep(10 * expiry)

	// Sanity check 1: no cache files
	requireCacheFiles(t, tmp, 0)
	// Sanity check 2: no index entries
	requireCacheEntries(t, c, 0)

	r2, created, err := c.FindOrCreate(key, writeString(content(2)))
	require.NoError(t, err)
	require.True(t, created, "created?")

	out2, err := ioutil.ReadAll(r2)
	require.NoError(t, err)
	require.NoError(t, r2.Close())

	// Sanity check 3: no stale value returned by the cache
	require.Equal(t, content(2), string(out2))
}
