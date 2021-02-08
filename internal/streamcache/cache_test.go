package streamcache

import (
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestWriteOneReadMultiple(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	c, err := NewCache(tmp, 0)
	require.NoError(t, err)

	const (
		key = "test key"
		N   = 10
	)
	content := func(i int) string { return fmt.Sprintf("content %d", i) }

	for i := 0; i < N; i++ {
		t.Run(fmt.Sprintf("read %d", i), func(t *testing.T) {
			r, err := c.FindOrCreate(key, func(w io.Writer) error { _, err := io.WriteString(w, content(i)); return err })
			require.NoError(t, err)

			out, err := ioutil.ReadAll(r)
			require.NoError(t, err)
			require.NoError(t, r.Close())
			require.Equal(t, content(0), string(out))
		})
	}

	cachedFiles := strings.Split(
		text.ChompBytes(testhelper.MustRunCommand(t, nil, "find", tmp, "-type", "f")),
		"\n",
	)
	require.Len(t, cachedFiles, 1, "number of files in cache")
	require.NotEmpty(t, cachedFiles[0])
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
		cache[i], err = NewCache(tmp, 0)
		require.NoError(t, err)

		reader[i], err = cache[i].FindOrCreate(key, func(w io.Writer) error { _, err := io.WriteString(w, input[i]); return err })
		require.NoError(t, err)
	}

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
