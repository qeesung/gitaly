package streamcache

import (
	"context"
	"errors"
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

func TestCache_writeOneReadMultiple(t *testing.T) {
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
			require.Equal(t, i == 0, created)
			defer r.Close()

			out, err := ioutil.ReadAll(r)
			require.NoError(t, err)
			require.NoError(t, r.Wait(context.Background()))
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
	t.Helper()

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
	t.Helper()
	c.m.Lock()
	defer c.m.Unlock()
	require.Len(t, c.index, n)
	require.Len(t, c.queue, n)
}

func TestCache_scope(t *testing.T) {
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
	reader := make([]*Stream, N)
	var err error

	for i := 0; i < N; i++ {
		input[i] = fmt.Sprintf("test content %d", i)
		cache[i], err = NewCache(tmp, time.Minute)
		require.NoError(t, err)
		defer func(i int) { cache[i].Stop() }(i)

		var created bool
		reader[i], created, err = cache[i].FindOrCreate(key, writeString(input[i]))
		require.NoError(t, err)
		require.True(t, created)
		defer func(i int) { reader[i].Close() }(i)
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
		require.NoError(t, r.Wait(context.Background()))

		require.Equal(t, content, string(out))
	}
}

func TestCache_diskCleanup(t *testing.T) {
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
	require.True(t, created)
	defer r1.Close()

	_, err = io.Copy(ioutil.Discard, r1)
	require.NoError(t, err)
	require.NoError(t, r1.Wait(context.Background()))

	requireCacheFiles(t, tmp, 1)
	requireCacheEntries(t, c, 1)

	time.Sleep(10 * expiry)

	// Sanity check 1: no cache files
	requireCacheFiles(t, tmp, 0)
	// Sanity check 2: no index entries
	requireCacheEntries(t, c, 0)

	r2, created, err := c.FindOrCreate(key, writeString(content(2)))
	require.NoError(t, err)
	require.True(t, created)
	defer r2.Close()

	out2, err := ioutil.ReadAll(r2)
	require.NoError(t, err)
	require.NoError(t, r2.Wait(context.Background()))

	// Sanity check 3: no stale value returned by the cache
	require.Equal(t, content(2), string(out2))
}

func TestCache_failedWrite(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	c, err := NewCache(tmp, time.Hour) // use very long expiry to prevent auto expiry
	require.NoError(t, err)
	defer c.Stop()

	testCases := []struct {
		desc   string
		create func(io.Writer) error
	}{
		{
			desc:   "create returns error",
			create: func(io.Writer) error { return errors.New("something went wrong") },
		},
		{
			desc:   "create panics",
			create: func(io.Writer) error { panic("oh no") },
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			r1, created, err := c.FindOrCreate(tc.desc, tc.create)
			require.NoError(t, err)
			require.True(t, created)

			_, err = io.Copy(ioutil.Discard, r1)
			require.NoError(t, err, "errors on the write end are not propagated via Read()")
			require.NoError(t, r1.Close(), "errors on the write end are not propagated via Close()")
			require.Error(t, r1.Wait(context.Background()), "error propagation happens via Wait()")

			time.Sleep(10 * time.Millisecond)

			const happy = "all is good"
			r2, created, err := c.FindOrCreate(tc.desc, writeString(happy))
			require.NoError(t, err)
			require.True(t, created, "because the previous entry failed, a new one should have been created")
			defer r2.Close()

			out, err := ioutil.ReadAll(r2)
			require.NoError(t, err)
			require.NoError(t, r2.Wait(context.Background()))
			require.Equal(t, happy, string(out))
		})
	}
}

func TestWaiter(t *testing.T) {
	w := newWaiter()
	errc := make(chan error, 1)
	go func() { errc <- w.Wait(context.Background()) }()

	err := errors.New("test error")
	w.SetError(err)
	require.Equal(t, err, <-errc)
}

func TestWaiter_cancel(t *testing.T) {
	w := newWaiter()
	errc := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { errc <- w.Wait(ctx) }()

	cancel()
	require.Equal(t, context.Canceled, <-errc)
}
