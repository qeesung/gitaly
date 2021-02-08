package streamcache

import (
	"fmt"
	"io"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestWriteOneReadOne(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	c, err := NewCache(tmp, 0)
	require.NoError(t, err)

	const (
		content = "test content"
		key     = "test key"
	)

	r, err := c.FindOrCreate(key, func(w io.Writer) error { _, err := io.WriteString(w, content); return err })
	require.NoError(t, err)

	out, err := ioutil.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, content, string(out))
}

func TestWriteOneReadMultiple(t *testing.T) {
	tmp, cleanTmp := testhelper.TempDir(t)
	defer cleanTmp()

	c, err := NewCache(tmp, 0)
	require.NoError(t, err)

	const (
		key = "test key"
	)
	content := func(i int) string { return fmt.Sprintf("content %d", i) }

	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprintf("read %d", i), func(t *testing.T) {
			r, err := c.FindOrCreate(key, func(w io.Writer) error { _, err := io.WriteString(w, content(i)); return err })
			require.NoError(t, err)

			out, err := ioutil.ReadAll(r)
			require.NoError(t, err)
			require.NoError(t, r.Close())
			require.Equal(t, content(0), string(out))
		})
	}
}
