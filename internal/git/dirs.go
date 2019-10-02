package git

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/log"
)

// ObjectDirectories looks for Git object directories, including
// alternates specified in objects/info/alternates.
func ObjectDirectories(repoPath string) ([]string, error) {
	objDir := filepath.Join(repoPath, "objects")
	dirs, err := altObjectDirs(objDir, 0)
	if err != nil {
		return nil, err
	}

	return dirs, nil
}

func altObjectDirs(objDir string, depth int) ([]string, error) {
	const maxAlternatesDepth = 5 // Taken from https://github.com/git/git/blob/v2.23.0/sha1-file.c#L575
	if depth > maxAlternatesDepth {
		log.Default().WithField("objdir", objDir).Warn("ignoring deeply nested alternate object directory")
		return nil, nil
	}

	fi, err := os.Stat(objDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, nil
	}

	dirs := []string{objDir}

	alternates, err := ioutil.ReadFile(filepath.Join(objDir, "info/alternates"))
	if os.IsNotExist(err) {
		return dirs, nil
	}
	if err != nil {
		return nil, err
	}

	for _, newDir := range strings.Split(string(alternates), "\n") {
		if len(newDir) == 0 || newDir[0] == '#' {
			continue
		}

		if !filepath.IsAbs(newDir) {
			newDir = filepath.Join(objDir, newDir)
		}

		nestedDirs, err := altObjectDirs(newDir, depth+1)
		if err != nil {
			return nil, err
		}

		dirs = append(dirs, nestedDirs...)
	}

	return dirs, nil
}
