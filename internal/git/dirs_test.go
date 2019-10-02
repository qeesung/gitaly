package git

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjectDirs(t *testing.T) {
	expected := []string{
		"testdata/objdirs/repo0/objects",
		"testdata/objdirs/repo1/objects",
		"testdata/objdirs/repo2/objects",
		"testdata/objdirs/repo3/objects",
		"testdata/objdirs/repo4/objects",
		"testdata/objdirs/repo5/objects",
		"testdata/objdirs/repoB/objects",
	}

	out, err := ObjectDirectories("testdata/objdirs/repo0")
	require.NoError(t, err)

	sort.Strings(out)
	require.Equal(t, expected, out)
}
