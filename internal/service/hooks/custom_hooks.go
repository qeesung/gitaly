package hook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/command"
)

// customHooksExecutor executes all custom hooks for a given repository and hook name
type customHooksExecutor func(ctx context.Context, args, env []string, stdin io.Reader, stdout, stderr io.Writer) error

// lookup hook files in this order:
//
// 1. <repository>.git/custom_hooks/<hook_name> - per project hook
// 2. <repository>.git/custom_hooks/<hook_name>.d/* - per project hooks
// 3. <repository>.git/hooks/<hook_name>.d/* - global hooks
func newCustomHooksExecutor(repoPath, customHooksDir, hookName string) (customHooksExecutor, error) {
	var hookFiles []string
	projectCustomHookFile := filepath.Join(repoPath, "custom_hooks", hookName)
	s, err := os.Stat(projectCustomHookFile)
	if err == nil && s.Mode()&0100 != 0 {
		hookFiles = append(hookFiles, projectCustomHookFile)
	}

	projectCustomHookDir := filepath.Join(repoPath, "custom_hooks", fmt.Sprintf("%s.d", hookName))
	files, err := matchFiles(projectCustomHookDir)
	if err != nil {
		return nil, err
	}
	hookFiles = append(hookFiles, files...)

	globalCustomHooksDir := filepath.Join(customHooksDir, fmt.Sprintf("%s.d", hookName))

	files, err = matchFiles(globalCustomHooksDir)
	if err != nil {
		return nil, err
	}
	hookFiles = append(hookFiles, files...)

	return func(ctx context.Context, args, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		stdinBytes, err := ioutil.ReadAll(stdin)
		if err != nil {
			return err
		}
		for _, hookFile := range hookFiles {
			c, err := command.New(ctx, exec.Command(hookFile, args...), bytes.NewBuffer(stdinBytes), stdout, stderr, env...)
			if err != nil {
				return err
			}
			if err = c.Wait(); err != nil {
				return err
			}
		}

		return nil
	}, nil
}

// match files from path:
// 1. file must be executable
// 2. file must not match backup file
//
// the resulting list is sorted
func matchFiles(dir string) ([]string, error) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var hookFiles []string

	for _, fi := range fis {
		if fi.IsDir() {
			continue
		}
		if fi.Mode()&0100 == 0 {
			continue
		}
		if strings.HasSuffix(fi.Name(), "~") {
			continue
		}
		hookFiles = append(hookFiles, filepath.Join(dir, fi.Name()))
	}

	sort.Strings(hookFiles)

	return hookFiles, nil
}
