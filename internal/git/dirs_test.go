package git

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestObjectDirs(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	expected := []string{
		"testdata/objdirs/repo0/objects",
		"testdata/objdirs/repo1/objects",
		"testdata/objdirs/repo2/objects",
		"testdata/objdirs/repo3/objects",
		"testdata/objdirs/repo4/objects",
		"testdata/objdirs/repo5/objects",
		"testdata/objdirs/repoB/objects",
	}

	out, err := ObjectDirectories(ctx, "testdata/objdirs/repo0")
	require.NoError(t, err)

	require.Equal(t, expected, out)
}
