package hooks

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

func TestPath(t *testing.T) {
	defer func(rubyDir string) {
		config.Config.Ruby.Dir = rubyDir
	}(config.Config.Ruby.Dir)
	config.Config.Ruby.Dir = "/bazqux/gitaly-ruby"

	t.Run("default", func(t *testing.T) {
		require.Equal(t, "/bazqux/gitaly-ruby/git-hooks", Path())
	})

	t.Run("with an override", func(t *testing.T) {
		Override = "/override/hooks"
		defer func() { Override = "" }()

		require.Equal(t, "/override/hooks", Path())
	})

	t.Run("when an env override", func(t *testing.T) {
		key := "GITALY_TESTING_NO_GIT_HOOKS"

		os.Setenv(key, "1")
		defer os.Unsetenv(key)

		require.Equal(t, "/var/empty", Path())
	})

}
