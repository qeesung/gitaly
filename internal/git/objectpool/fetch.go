package objectpool

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
)

var objectPoolRefspec = fmt.Sprintf("+refs/*:%s/*", git.ObjectPoolRefNamespace)

// FetchFromOrigin initializes the pool and fetches the objects from its origin repository
func (o *ObjectPool) FetchFromOrigin(ctx context.Context, origin *localrepo.Repo) error {
	if err := o.Init(ctx); err != nil {
		return fmt.Errorf("initializing object pool: %w", err)
	}

	originPath, err := origin.Path()
	if err != nil {
		return fmt.Errorf("computing origin repo's path: %w", err)
	}

	if err := o.housekeepingManager.CleanStaleData(ctx, o.Repo); err != nil {
		return fmt.Errorf("cleaning stale data: %w", err)
	}

	if err := o.logStats(ctx, "before fetch"); err != nil {
		return fmt.Errorf("computing stats before fetch: %w", err)
	}

	// Ideally we wouldn't want to prune old references at all so that we can keep alive all
	// objects without having to create loads of dangling references. But unfortunately keeping
	// around old refs can lead to D/F conflicts between old references that have since
	// been deleted in the pool and new references that have been added in the pool member we're
	// fetching from. E.g. if we have the old reference `refs/heads/branch` and the pool member
	// has replaced that since with a new reference `refs/heads/branch/conflict` then
	// the fetch would now always fail because of that conflict.
	//
	// Due to the lack of an alternative to resolve that conflict we are thus forced to enable
	// pruning. This isn't too bad given that we know to keep alive the old objects via dangling
	// refs anyway, but I'd sleep easier if we didn't have to do this.
	//
	// Note that we need to perform the pruning separately from the fetch: if the fetch is using
	// `--atomic` and `--prune` together then it still wouldn't be able to recover from the D/F
	// conflict. So we first to a preliminary prune that only prunes refs without fetching
	// objects yet to avoid that scenario.
	if featureflag.FetchIntoObjectPoolPruneRefs.IsEnabled(ctx) {
		if err := o.pruneReferences(ctx, origin); err != nil {
			return fmt.Errorf("pruning references: %w", err)
		}
	}

	var stderr bytes.Buffer
	if err := o.Repo.ExecAndWait(ctx,
		git.SubCmd{
			Name: "fetch",
			Flags: []git.Option{
				git.Flag{Name: "--quiet"},
				git.Flag{Name: "--atomic"},
				// We already fetch tags via our refspec, so we don't
				// want to fetch them a second time via Git's default
				// tag refspec.
				git.Flag{Name: "--no-tags"},
				// We don't need FETCH_HEAD, and it can potentially be hundreds of
				// megabytes when doing a mirror-sync of repos with huge numbers of
				// references.
				git.Flag{Name: "--no-write-fetch-head"},
				// Disable showing forced updates, which may take a considerable
				// amount of time to compute. We don't display any output anyway,
				// which makes this computation kind of moot.
				git.Flag{Name: "--no-show-forced-updates"},
			},
			Args: []string{originPath, objectPoolRefspec},
		},
		git.WithRefTxHook(o.Repo),
		git.WithStderr(&stderr),
		git.WithConfig(git.ConfigPair{
			// Git is so kind to point out that we asked it to not show forced updates
			// by default, so we need to ask it not to do that.
			Key: "advice.fetchShowForcedUpdates", Value: "false",
		}),
	); err != nil {
		return fmt.Errorf("fetch into object pool: %w, stderr: %q", err,
			stderr.String())
	}

	if err := o.rescueDanglingObjects(ctx); err != nil {
		return fmt.Errorf("rescuing dangling objects: %w", err)
	}

	if err := o.logStats(ctx, "after fetch"); err != nil {
		return fmt.Errorf("computing stats after fetch: %w", err)
	}

	if err := o.housekeepingManager.OptimizeRepository(ctx, o.Repo); err != nil {
		return fmt.Errorf("optimizing pool repo: %w", err)
	}

	return nil
}

// pruneReferences prunes any references that have been deleted in the origin repository.
func (o *ObjectPool) pruneReferences(ctx context.Context, origin *localrepo.Repo) error {
	originPath, err := origin.Path()
	if err != nil {
		return fmt.Errorf("computing origin repo's path: %w", err)
	}

	// Ideally, we'd just use `git remote prune` directly. But unfortunately, this command does
	// not support atomic updates, but will instead use a separate reference transaction for
	// updating the packed-refs file and for updating each of the loose references. This can be
	// really expensive in case we are about to prune a lot of references given that every time,
	// the reference-transaction hook needs to vote on the deletion and reach quorum.
	//
	// Instead we ask for a dry-run, parse the output and queue up every reference into a
	// git-update-ref(1) process. While ugly, it works around the performance issues.
	prune, err := o.Repo.Exec(ctx,
		git.SubSubCmd{
			Name:   "remote",
			Action: "prune",
			Args:   []string{"origin"},
			Flags: []git.Option{
				git.Flag{Name: "--dry-run"},
			},
		},
		git.WithConfig(git.ConfigPair{Key: "remote.origin.url", Value: originPath}),
		git.WithConfig(git.ConfigPair{Key: "remote.origin.fetch", Value: objectPoolRefspec}),
		// This is a dry-run, only, so we don't have to enable hooks.
		git.WithDisabledHooks(),
	)
	if err != nil {
		return fmt.Errorf("spawning prune: %w", err)
	}

	updater, err := updateref.New(ctx, o.Repo)
	if err != nil {
		return fmt.Errorf("spawning updater: %w", err)
	}

	// We need to manually compute a vote because all deletions we queue up here are
	// force-deletions. We are forced to filter out force-deletions because these may also
	// happen when evicting references from the packed-refs file.
	voteHash := voting.NewVoteHash()

	scanner := bufio.NewScanner(prune)
	for scanner.Scan() {
		line := scanner.Bytes()

		// We need to skip the first two lines that represent the header of git-remote(1)'s
		// output. While we should ideally just use a state machine here, it doesn't feel
		// worth it given that the output is comparatively simple and given that the pruned
		// branches are distinguished by a special prefix.
		switch {
		case bytes.Equal(line, []byte("Pruning origin")):
			continue
		case bytes.HasPrefix(line, []byte("URL: ")):
			continue
		case bytes.HasPrefix(line, []byte(" * [would prune] ")):
			// The references announced by git-remote(1) only have the remote's name as
			// prefix, which is "origin". We thus have to reassemble the complete name
			// of every reference here.
			deletedRef := "refs/remotes/" + string(bytes.TrimPrefix(line, []byte(" * [would prune] ")))

			if _, err := io.Copy(voteHash, strings.NewReader(fmt.Sprintf("%[1]s %[1]s %s\n", git.ObjectHashSHA1.ZeroOID, deletedRef))); err != nil {
				return fmt.Errorf("hashing reference deletion: %w", err)
			}

			if err := updater.Delete(git.ReferenceName(deletedRef)); err != nil {
				return fmt.Errorf("queueing ref for deletion: %w", err)
			}
		default:
			return fmt.Errorf("unexpected line: %q", line)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning deleted refs: %w", err)
	}

	if err := prune.Wait(); err != nil {
		return fmt.Errorf("waiting for prune: %w", err)
	}

	vote, err := voteHash.Vote()
	if err != nil {
		return fmt.Errorf("computing vote: %w", err)
	}

	// Prepare references so that they're locked and cannot be written by any concurrent
	// processes. This also verifies that we can indeed delete the references.
	if err := updater.Prepare(); err != nil {
		return fmt.Errorf("preparing deletion of references: %w", err)
	}

	// Vote on the references we're about to delete.
	if err := transaction.VoteOnContext(ctx, o.txManager, vote, voting.Prepared); err != nil {
		return fmt.Errorf("preparational vote on pruned references: %w", err)
	}

	// Commit the pruned references to disk so that the change gets applied.
	if err := updater.Commit(); err != nil {
		return fmt.Errorf("deleting references: %w", err)
	}

	// And then confirm that we actually deleted the references.
	if err := transaction.VoteOnContext(ctx, o.txManager, vote, voting.Committed); err != nil {
		return fmt.Errorf("preparational vote on pruned references: %w", err)
	}

	return nil
}

const danglingObjectNamespace = "refs/dangling/"

// rescueDanglingObjects creates refs for all dangling objects if finds
// with `git fsck`, which converts those objects from "dangling" to
// "not-dangling". This guards against any object ever being deleted from
// a pool repository. This is a defense in depth against accidental use
// of `git prune`, which could remove Git objects that a pool member
// relies on. There is currently no way for us to reliably determine if
// an object is still used anywhere, so the only safe thing to do is to
// assume that every object _is_ used.
func (o *ObjectPool) rescueDanglingObjects(ctx context.Context) error {
	fsck, err := o.Repo.Exec(ctx, git.SubCmd{
		Name:  "fsck",
		Flags: []git.Option{git.Flag{Name: "--connectivity-only"}, git.Flag{Name: "--dangling"}},
	})
	if err != nil {
		return err
	}

	updater, err := updateref.New(ctx, o.Repo, updateref.WithDisabledTransactions())
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

		danglingObjectID, err := git.ObjectHashSHA1.FromHex(split[2])
		if err != nil {
			return fmt.Errorf("parsing object ID %q: %w", split[2], err)
		}

		ref := git.ReferenceName(danglingObjectNamespace + split[2])
		if err := updater.Create(ref, danglingObjectID); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if err := fsck.Wait(); err != nil {
		return fmt.Errorf("git fsck: %v", err)
	}

	return updater.Commit()
}

func (o *ObjectPool) logStats(ctx context.Context, when string) error {
	fields := logrus.Fields{
		"when": when,
	}

	for key, dir := range map[string]string{
		"poolObjectsSize": "objects",
		"poolRefsSize":    "refs",
	} {
		var err error
		fields[key], err = sizeDir(ctx, filepath.Join(o.FullPath(), dir))
		if err != nil {
			return err
		}
	}

	forEachRef, err := o.Repo.Exec(ctx, git.SubCmd{
		Name:  "for-each-ref",
		Flags: []git.Option{git.Flag{Name: "--format=%(objecttype)%00%(refname)"}},
		Args:  []string{"refs/"},
	})
	if err != nil {
		return err
	}

	danglingRefsByType := make(map[string]int)
	normalRefsByType := make(map[string]int)

	scanner := bufio.NewScanner(forEachRef)
	for scanner.Scan() {
		line := bytes.SplitN(scanner.Bytes(), []byte{0}, 2)
		if len(line) != 2 {
			continue
		}

		objectType := string(line[0])
		refname := string(line[1])

		if strings.HasPrefix(refname, danglingObjectNamespace) {
			danglingRefsByType[objectType]++
		} else {
			normalRefsByType[objectType]++
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if err := forEachRef.Wait(); err != nil {
		return err
	}

	for _, key := range []string{"blob", "commit", "tag", "tree"} {
		fields["dangling."+key+".ref"] = danglingRefsByType[key]
		fields["normal."+key+".ref"] = normalRefsByType[key]
	}

	ctxlogrus.Extract(ctx).WithFields(fields).Info("pool dangling ref stats")

	return nil
}

func sizeDir(ctx context.Context, dir string) (int64, error) {
	// du -k reports size in KB
	cmd, err := command.New(ctx, []string{"du", "-sk", dir})
	if err != nil {
		return 0, err
	}

	sizeLine, err := io.ReadAll(cmd)
	if err != nil {
		return 0, err
	}

	if err := cmd.Wait(); err != nil {
		return 0, err
	}

	sizeParts := bytes.Split(sizeLine, []byte("\t"))
	if len(sizeParts) != 2 {
		return 0, fmt.Errorf("malformed du output: %q", sizeLine)
	}

	size, err := strconv.ParseInt(string(sizeParts[0]), 10, 0)
	if err != nil {
		return 0, err
	}

	// Convert KB to B
	return size * 1024, nil
}
