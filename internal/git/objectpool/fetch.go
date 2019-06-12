package objectpool

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

// FetchFromOrigin initializes the pool and fetches the objects from its origin repository
func (o *ObjectPool) FetchFromOrigin(ctx context.Context, origin *gitalypb.Repository) error {
	if err := o.Init(ctx); err != nil {
		return err
	}

	originPath, err := helper.GetPath(origin)

	if err != nil {
		return err
	}

	getRemotes, err := git.Command(ctx, o, "remote")
	if err != nil {
		return err
	}

	remoteReader := bufio.NewScanner(getRemotes)

	var originExists bool
	for remoteReader.Scan() {
		if remoteReader.Text() == "origin" {
			originExists = true
		}
	}
	if err := getRemotes.Wait(); err != nil {
		return err
	}

	var setOriginCmd *command.Command
	if originExists {
		setOriginCmd, err = git.Command(ctx, o, "remote", "set-url", "origin", originPath)
		if err != nil {
			return err
		}
	} else {
		setOriginCmd, err = git.Command(ctx, o, "remote", "add", "origin", originPath)
		if err != nil {
			return err
		}
	}

	if err := setOriginCmd.Wait(); err != nil {
		return err
	}

	fetchCmd, err := git.Command(ctx, o, "fetch", "--quiet", "origin")
	if err != nil {
		return err
	}

	if err := fetchCmd.Wait(); err != nil {
		return err
	}

	if err := rescueDanglingObjects(ctx, o); err != nil {
		return err
	}

	return nil
}

const danglingObjectNamespace = "refs/dangling"

func rescueDanglingObjects(ctx context.Context, repo repository.GitRepo) error {
	fsck, err := git.Command(ctx, repo, "fsck", "--connectivity-only", "--dangling")
	if err != nil {
		return err
	}

	updater, err := updateref.New(ctx, repo)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(fsck)
	for scanner.Scan() {
		split := strings.SplitN(scanner.Text(), " ", 3)
		if len(split) != 3 {
			continue
		}

		if split[0] != "dangling" {
			continue
		}

		ref := danglingObjectNamespace + "/" + split[2]
		if err := updater.Create(ref, split[2]); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := fsck.Wait(); err != nil {
		return fmt.Errorf("git fsck: %v", err)
	}

	if err := updater.Wait(); err != nil {
		return err
	}

	return nil
}
