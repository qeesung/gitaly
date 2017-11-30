package alternates

import (
	"fmt"
	"path"
	"strings"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

// PathAndEnv finds the disk path to a repository, and returns the
// alternate object directory environment variables encoded in the
// pb.Repository instance.
func PathAndEnv(repo *pb.Repository) (string, []string, error) {
	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return "", nil, err
	}

	var env []string
	if dir := repo.GetGitObjectDirectory(); dir != "" {
		env = append(env, fmt.Sprintf("GIT_OBJECT_DIRECTORY=%s", path.Join(repoPath, dir)))
	}

	if dirs := repo.GetGitAlternateObjectDirectories(); len(dirs) > 0 {
		var dirsList []string

		for _, dir := range dirs {
			dirsList = append(dirsList, path.Join(repoPath, dir))
		}

		env = append(env, fmt.Sprintf("GIT_ALTERNATE_OBJECT_DIRECTORIES=%s", strings.Join(dirsList, ":")))
	}

	return repoPath, env, nil
}
