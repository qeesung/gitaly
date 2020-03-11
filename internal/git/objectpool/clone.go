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

	cmd, err := git.SafeCmdWithoutRepo(ctx,
		[]git.Option{
			git.ValueFlag{
				Name:  "-C",
				Value: o.storagePath,
			},
		},
		git.SubCmd{
			Name: "clone",
			Flags: []git.Option{
				git.Flag{Name: "--quiet"},
				git.Flag{Name: "--bare"},
				git.Flag{Name: "--local"},
			},
			Args: []string{repoPath, o.relativePath},
		},
	)
	if err != nil {
		return err
	}

	return cmd.Wait()
}

func (o *ObjectPool) removeHooksDir() error {
	return os.RemoveAll(path.Join(o.FullPath(), "hooks"))
}
