package hooks

import (
	"os"
	"path"

	"gitlab.com/gitlab-org/gitaly/internal/config"
)

// Override allows tests to control where the hooks directory is. Outside
// consumers can also use the GITALY_TESTING_HOOKS_OVERRIDE environment
// variable.
var Override string

// Path returns the path where the global git hooks are located.
func Path() string {
	if len(Override) > 0 {
		return Override
	}

	if dir := os.Getenv("GITALY_TESTING_HOOKS_DIRECTORY"); len(dir) > 0 {
		return dir
	}

	return path.Join(config.Config.Ruby.Dir, "git-hooks")
}
