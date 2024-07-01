package repository

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) FetchRemote(ctx context.Context, req *gitalypb.FetchRemoteRequest) (*gitalypb.FetchRemoteResponse, error) {
	if err := s.validateFetchRemoteRequest(ctx, req); err != nil {
		return nil, err
	}

	if req.GetTimeout() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.GetTimeout())*time.Second)
		defer cancel()
	}

	tagsChanged, err := s.fetchRemoteAtomic(ctx, req)
	if err != nil {
		return nil, err
	}

	return &gitalypb.FetchRemoteResponse{TagsChanged: tagsChanged}, nil
}

// fetchRemoteAtomic fetches changes from the specified remote repository. To be atomic, fetched
// objects are first quarantined and only migrated before committing the reference transaction.
func (s *server) fetchRemoteAtomic(ctx context.Context, req *gitalypb.FetchRemoteRequest) (_ bool, returnedErr error) {
	var stdout, stderr bytes.Buffer
	opts := localrepo.FetchOpts{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Force:   req.Force,
		Prune:   !req.NoPrune,
		Tags:    localrepo.FetchOptsTagsAll,
		Verbose: true,
		// Transactions are disabled during fetch operation because no references are updated when
		// the dry-run option is enabled. Instead, the reference-transaction hook is performed
		// during the subsequent execution of `git-update-ref(1)`.
		DisableTransactions: true,
		// When the `dry-run` option is used with `git-fetch(1)`, Git objects are received without
		// performing reference updates. This is used to quarantine objects on the initial fetch and
		// migration to occur only during reference update.
		DryRun: true,
		// The `porcelain` option outputs reference update information from `git-fetch(1) to stdout.
		// Since references are not updated during a `git-fetch(1)` dry-run, the reference
		// information is used during `git-update-ref(1)` execution to update the appropriate
		// corresponding references.
		Porcelain: true,
	}

	if req.GetNoTags() {
		opts.Tags = localrepo.FetchOptsTagsNone
	}

	if err := buildCommandOpts(&opts, req); err != nil {
		return false, err
	}

	sshCommand, cleanup, err := git.BuildSSHInvocation(ctx, s.logger, req.GetSshKey(), req.GetKnownHosts())
	if err != nil {
		return false, err
	}
	defer cleanup()

	opts.Env = append(opts.Env, "GIT_SSH_COMMAND="+sshCommand)

	// When performing fetch, objects are received before references are updated. If references fail
	// to be updated, unreachable objects could be left in the repository that would need to be
	// garbage collected. To be more atomic, a quarantine directory is set up where objects will be
	// fetched prior to being migrated to the main repository when reference updates are committed.
	quarantineDir, err := quarantine.New(ctx, req.GetRepository(), s.logger, s.locator)
	if err != nil {
		return false, fmt.Errorf("creating quarantine directory: %w", err)
	}

	quarantineRepo := s.localrepo(quarantineDir.QuarantinedRepo())
	if err := quarantineRepo.FetchRemote(ctx, "inmemory", opts); err != nil {
		// When `git-fetch(1)` fails to apply all reference updates successfully, the command
		// returns `exit status 1`. Despite this error, successful reference updates should still be
		// applied during the subsequent `git-update-ref(1)`. To differentiate between regular
		// errors and failed reference updates, stderr is checked for an error message. If an error
		// message is present, it is determined that an error occurred and the operation halts.
		errMsg := stderr.String()
		if errMsg != "" {
			return false, structerr.NewInternal("fetch remote: %q: %w", errMsg, err)
		}

		// Some errors during the `git-fetch(1)` operation do not print to stderr. If the error
		// message is not `exit status 1`, it is determined that the error is unrelated to failed
		// reference updates and the operation halts. Otherwise, it is assumed the error is from a
		// failed reference update and the operation proceeds to update references.
		if err.Error() != "exit status 1" {
			return false, structerr.NewInternal("fetch remote: %w", err)
		}
	}

	// A repository cannot contain references with F/D (file/directory) conflicts (i.e.
	// `refs/heads/foo` and `refs/heads/foo/bar`). If fetching from the remote repository
	// results in an F/D conflict, the reference update fails. In some cases a conflicting
	// reference may exist locally that does not exist on the remote. In this scenario, if
	// outdated references are first pruned locally, the F/D conflict can be avoided. When
	// `git-fetch(1)` is performed with the `--prune` and `--dry-run` flags, the pruned
	// references are also included in the output without performing any actual reference
	// updates. Bulk atomic reference updates performed by `git-update-ref(1)` do not support
	// F/D conflicts even if the conflicted reference is being pruned. Therefore, pruned
	// references must be updated first in a separate transaction. To accommodate this, two
	// different instances of `updateref.Updater` are used to keep the transactions separate.
	prunedUpdater, err := updateref.New(ctx, quarantineRepo)
	if err != nil {
		return false, fmt.Errorf("spawning pruned updater: %w", err)
	}
	defer func() {
		if err := prunedUpdater.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("cancel pruned updater: %w", err)
		}
	}()

	// All other reference updates can be queued as part of the same transaction.
	refUpdater, err := updateref.New(ctx, quarantineRepo)
	if err != nil {
		return false, fmt.Errorf("spawning ref updater: %w", err)
	}
	defer func() {
		if err := refUpdater.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("cancel ref updater: %w", err)
		}
	}()

	if err := prunedUpdater.Start(); err != nil {
		return false, fmt.Errorf("start reference transaction: %w", err)
	}

	if err := refUpdater.Start(); err != nil {
		return false, fmt.Errorf("start reference transaction: %w", err)
	}

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return false, fmt.Errorf("detecting object hash: %w", err)
	}

	var tagsChanged bool

	// Parse stdout to identify required reference updates. Reference updates are queued to the
	// respective updater based on type.
	scanner := git.NewFetchPorcelainScanner(&stdout, objectHash)
	for scanner.Scan() {
		status := scanner.StatusLine()

		switch status.Type {
		// Failed and unchanged reference updates do not need to be applied.
		case git.RefUpdateTypeUpdateFailed, git.RefUpdateTypeUnchanged:
		// Queue pruned references in a separate transaction to avoid F/D conflicts.
		case git.RefUpdateTypePruned:
			if err := prunedUpdater.Delete(git.ReferenceName(status.Reference)); err != nil {
				return false, fmt.Errorf("queueing pruned ref for deletion: %w", err)
			}
		// Queue all other reference updates in the same transaction.
		default:
			if err := refUpdater.Update(git.ReferenceName(status.Reference), status.NewOID, status.OldOID); err != nil {
				return false, fmt.Errorf("queueing ref to be updated: %w", err)
			}

			// While scanning reference updates, check if any tags changed.
			if status.Type == git.RefUpdateTypeTagUpdate || (status.Type == git.RefUpdateTypeFetched && strings.HasPrefix(status.Reference, "refs/tags")) {
				tagsChanged = true
			}
		}
	}
	if scanner.Err() != nil {
		return false, fmt.Errorf("scanning fetch output: %w", scanner.Err())
	}

	// Prepare pruned references in separate transaction to avoid F/D conflicts.
	if err := prunedUpdater.Prepare(); err != nil {
		return false, fmt.Errorf("preparing reference prune: %w", err)
	}

	// Commit pruned references to complete transaction and apply changes.
	if err := prunedUpdater.Commit(); err != nil {
		return false, fmt.Errorf("committing reference prune: %w", err)
	}

	// Prepare the remaining queued reference updates.
	if err := refUpdater.Prepare(); err != nil {
		return false, fmt.Errorf("preparing reference update: %w", err)
	}

	// Before committing the remaining reference updates, fetched objects must be migrated out of
	// the quarantine directory.
	if err := quarantineDir.Migrate(ctx); err != nil {
		return false, fmt.Errorf("migrating quarantined objects: %w", err)
	}

	// Commit the remaining queued reference updates so the changes get applied.
	if err := refUpdater.Commit(); err != nil {
		return false, fmt.Errorf("committing reference update: %w", err)
	}

	if req.GetCheckTagsChanged() {
		return tagsChanged, nil
	}

	// If the request does not specify to check if tags changed, return true as the default value.
	return true, nil
}

func buildCommandOpts(opts *localrepo.FetchOpts, req *gitalypb.FetchRemoteRequest) error {
	remoteURL := req.GetRemoteParams().GetUrl()
	var config []git.ConfigPair

	for _, refspec := range getRefspecs(req.GetRemoteParams().GetMirrorRefmaps()) {
		config = append(config, git.ConfigPair{
			Key: "remote.inmemory.fetch", Value: refspec,
		})
	}

	if resolvedAddress := req.GetRemoteParams().GetResolvedAddress(); resolvedAddress != "" {
		modifiedURL, resolveConfig, err := git.GetURLAndResolveConfig(remoteURL, resolvedAddress)
		if err != nil {
			return fmt.Errorf("couldn't get curloptResolve config: %w", err)
		}

		remoteURL = modifiedURL
		config = append(config, resolveConfig...)
	}

	config = append(config, git.ConfigPair{Key: "remote.inmemory.url", Value: remoteURL})

	if authHeader := req.GetRemoteParams().GetHttpAuthorizationHeader(); authHeader != "" {
		config = append(config, git.ConfigPair{
			Key:   fmt.Sprintf("http.%s.extraHeader", req.GetRemoteParams().GetUrl()),
			Value: "Authorization: " + authHeader,
		})
	}

	opts.CommandOptions = append(opts.CommandOptions, git.WithConfigEnv(config...))

	return nil
}

func (s *server) validateFetchRemoteRequest(ctx context.Context, req *gitalypb.FetchRemoteRequest) error {
	if err := s.locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if req.GetRemoteParams() == nil {
		return structerr.NewInvalidArgument("missing remote params")
	}

	if req.GetRemoteParams().GetUrl() == "" {
		return structerr.NewInvalidArgument("blank or empty remote URL")
	}

	return nil
}

func getRefspecs(refmaps []string) []string {
	if len(refmaps) == 0 {
		return []string{"refs/*:refs/*"}
	}

	refspecs := make([]string, 0, len(refmaps))

	for _, refmap := range refmaps {
		switch refmap {
		case "all_refs":
			// with `all_refs`, the repository is equivalent to the result of `git clone --mirror`
			refspecs = append(refspecs, "refs/*:refs/*")
		case "heads":
			refspecs = append(refspecs, "refs/heads/*:refs/heads/*")
		case "tags":
			refspecs = append(refspecs, "refs/tags/*:refs/tags/*")
		default:
			refspecs = append(refspecs, refmap)
		}
	}
	return refspecs
}
