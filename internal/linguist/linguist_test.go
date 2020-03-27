package linguist

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(m.Run())
}

func TestLoadLanguages(t *testing.T) {
	colorMap = make(map[string]Language)
	require.NoError(t, LoadColors(config.Config), "load colors")

	require.Equal(t, "#701516", Color("Ruby"), "color value for 'Ruby'")
}

func TestLoadLanguagesCustomPath(t *testing.T) {
	jsonPath, err := filepath.Abs("testdata/fake-languages.json")
	require.NoError(t, err)

	config.Config.Ruby.LinguistLanguagesPath = jsonPath

	colorMap = make(map[string]Language)
	require.NoError(t, LoadColors(config.Config), "load colors")

	require.Equal(t, "foo color", Color("FooBar"))
}
