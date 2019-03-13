package hooks

import (
	"path"

	"gitlab.com/gitlab-org/gitaly/internal/config"
)

// Override allows tests to control where the hooks directory is.
var Override string

// Path returns the path where the global git hooks are located.
func Path() string {
	if len(Override) > 0 {
		return Override
	}

	return path.Join(config.Config.Ruby.Dir, "git-hooks")
}
