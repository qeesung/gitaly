package objectpool

import (
	"context"
	"os"
	"path"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// Clone a repository to a pool, without setting the alternates, is not the
// resposibility of this function.
func (o *ObjectPool) clone(ctx context.Context, repo *gitalypb.Repository) error {
	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return err
	}

	targetDir := o.FullPath()
	targetName := path.Base(targetDir)
	if err != nil {
		return err
	}

	cloneArgs := []string{"-C", path.Dir(targetDir), "clone", "--quiet", "--bare", "--local", repoPath, targetName}
	cmd, err := git.CommandWithoutRepo(ctx, cloneArgs...)
	if err != nil {
		return err
	}

	return cmd.Wait()
}

func (o *ObjectPool) removeHooksDir() error {
	return os.RemoveAll(path.Join(o.FullPath(), "hooks"))
}
