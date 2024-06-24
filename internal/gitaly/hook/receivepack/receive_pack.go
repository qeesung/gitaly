package receivepack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// RegisterProcReceiveHook is intended for use by the receive-pack RPC handlers when transactions
// are enabled to coordinate use and cleanup of the proc-receive hook handler. The returned cleanup
// function is required to be executed to ensure the goroutine is stopped.
func RegisterProcReceiveHook(
	ctx context.Context,
	logger log.Logger,
	cfg config.Cfg,
	req git.ReceivePackRequest,
	repo *localrepo.Repo,
	hookManager hook.Manager,
	txRegistry hook.TransactionRegistry,
	transactionID storage.TransactionID,
	stdout, stderr io.Writer,
) (func() error, error) {
	receiveDoneCh := make(chan struct{})
	handlerErrCh := make(chan error, 1)

	tx, err := txRegistry.Get(transactionID)
	if err != nil {
		return nil, fmt.Errorf("getting transaction: %w", err)
	}

	registry := hookManager.ProcReceiveRegistry()
	handlerCh, cleanup, err := registry.RegisterWaiter(transactionID)
	if err != nil {
		return nil, fmt.Errorf("registering waiter: %w", err)
	}

	go func() {
		select {
		case <-ctx.Done():
		case <-receiveDoneCh:
		case handler := <-handlerCh:
			if err := procReceiveHook(ctx, logger, cfg, req, repo, hookManager, tx, handler, stdout, stderr); err != nil {
				handlerErrCh <- err
			}
		}
		close(handlerErrCh)
	}()

	return func() error {
		cleanup()

		// To check for a proc-receive handler error, ensure the goroutine is not blocked.
		close(receiveDoneCh)
		if err, ok := <-handlerErrCh; ok {
			return err
		}

		return nil
	}, nil
}

// procReceiveHook is intended for use by the receive-pack RPC handlers during the
// proc-receive hook execution to handle atomic and non-atomic reference updates. If the
// proc-receive handler indicates the operation is atomic, references updates are all or nothing.
// Any errors result in the transaction failing. If the operation is non-atomic, references are
// updated one at a time. If a single reference update error is due to an update hook failure, the
// reference update is rejected, but other updates are able to proceed.
func procReceiveHook(
	ctx context.Context,
	logger log.Logger,
	cfg config.Cfg,
	req git.ReceivePackRequest,
	repo *localrepo.Repo,
	hookManager hook.Manager,
	tx hook.Transaction,
	handler hook.ProcReceiveHandler,
	stdout, stderr io.Writer,
) (returnedErr error) {
	var acceptedUpdates []hook.ReferenceUpdate
	rejectedUpdates := make(map[git.ReferenceName]string)

	defer func() {
		if err := handler.Close(returnedErr); err != nil && returnedErr == nil {
			returnedErr = err
		}
	}()

	if handler.Atomic() {
		// Atomic reference updates are all or nothing. If this fails for any reason, return an error.
		err := receivePackReferenceUpdates(ctx, cfg, req, repo, hookManager, handler.ReferenceUpdates(), stdout, stderr)
		if err != nil {
			return fmt.Errorf("updating references atomically: %w", err)
		}

		// There is no need to track rejected reference updates because we fail early if any updates
		// are not successful.
		acceptedUpdates = handler.ReferenceUpdates()
	} else {
		// Non-atomic reference updates are performed one at a time. Errors due to an update hook
		// failing are expected and should signal to the client it was rejected instead of
		// completely failing.
		for _, update := range handler.ReferenceUpdates() {
			if err := receivePackReferenceUpdates(ctx, cfg, req, repo, hookManager, []hook.ReferenceUpdate{update}, stdout, stderr); err != nil {
				var (
					reason    string
					hookErr   hook.CustomHookError
					updateErr updateError
				)
				switch {
				case errors.As(err, &hookErr):
					reason = "update hook failed"
				case errors.As(err, &updateErr):
					reason = updateErr.Error()
				default:
					return fmt.Errorf("updating reference: %w", err)
				}

				rejectedUpdates[update.Ref] = reason
			} else {
				acceptedUpdates = append(acceptedUpdates, update)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	for _, update := range handler.ReferenceUpdates() {
		if reason, rejected := rejectedUpdates[update.Ref]; rejected {
			if err := handler.RejectUpdate(update.Ref, reason); err != nil {
				return fmt.Errorf("rejecting update: %w", err)
			}
		} else {
			if err := handler.AcceptUpdate(update.Ref); err != nil {
				return fmt.Errorf("accepting update: %w", err)
			}
		}
	}

	// The post-receive hook only executes if there are successful updates.
	if len(acceptedUpdates) > 0 {
		hooksPayload, err := setupHooksPayloadEnv(ctx, cfg, req, repo, git.PostReceiveHook)
		if err != nil {
			return fmt.Errorf("creating hooks payload: %w", err)
		}

		var changes strings.Builder
		for _, update := range acceptedUpdates {
			changes.WriteString(fmt.Sprintf("%s %s %s\n", update.OldOID, update.NewOID, update.Ref))
		}

		var customHookErr hook.CustomHookError
		if err := hookManager.PostReceiveHook(
			ctx,
			req.GetRepository(),
			handler.PushOptions(),
			[]string{hooksPayload},
			strings.NewReader(changes.String()),
			stdout,
			stderr,
		); err != nil {
			if errors.As(err, &customHookErr) {
				// Only log the error when we've got a custom-hook error.
				logger.WithError(err).ErrorContext(ctx, "custom post-receive hook returned an error")
			} else {
				return fmt.Errorf("running post-receive hooks: %w", err)
			}
		}
	}

	return nil
}

// receivePackReferenceUpdates is intended for use with receive-pack RPC handlers when executing the
// proc-receive hook. In addition to atomically updating references in bulk, it also manually
// invokes the update hook for each reference since the proc-receive hook disables the normal
// automatic execution of the update hook. Errors result in reference updates not being committed.
// If the error is due to update hook failure, a CustomHookError is returned to provide context
// regarding why the update hook failed.
func receivePackReferenceUpdates(
	ctx context.Context,
	cfg config.Cfg,
	req git.ReceivePackRequest,
	repo *localrepo.Repo,
	hookManager hook.Manager,
	updates []hook.ReferenceUpdate,
	stdout, stderr io.Writer,
) (returnedErr error) {
	hooksPayload, err := setupHooksPayloadEnv(ctx, cfg, req, repo, git.UpdateHook)
	if err != nil {
		return fmt.Errorf("creating hooks payload: %w", err)
	}

	updater, err := updateref.New(ctx, repo, updateref.WithNoDeref())
	if err != nil {
		return fmt.Errorf("spawning ref updater: %w", err)
	}
	defer func() {
		if err := updater.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("cancel ref updater: %w", err)
		}
	}()

	if err := updater.Start(); err != nil {
		return fmt.Errorf("start reference transaction: %w", err)
	}

	for _, update := range updates {
		if err := hookManager.UpdateHook(
			ctx,
			req.GetRepository(),
			update.Ref.String(),
			update.OldOID.String(),
			update.NewOID.String(),
			[]string{hooksPayload},
			stdout, stderr,
		); err != nil {
			return fmt.Errorf("running update hook: %w", err)
		}

		if err := updater.Update(update.Ref, update.NewOID, update.OldOID); err != nil {
			return fmt.Errorf("queueing ref to be updated: %w", err)
		}
	}

	if err := updater.Commit(); err != nil {
		return updateError{error: err}
	}

	return nil
}

func setupHooksPayloadEnv(ctx context.Context, cfg config.Cfg, req git.ReceivePackRequest, repo *localrepo.Repo, hook git.Hook) (string, error) {
	var protocol string
	switch req.(type) {
	case *gitalypb.SSHReceivePackRequest:
		protocol = "ssh"
	case *gitalypb.PostReceivePackRequest:
		protocol = "http"
	}

	var praefectTx *txinfo.Transaction
	if tx, err := txinfo.TransactionFromContext(ctx); err == nil {
		praefectTx = &tx
	} else if !errors.Is(err, txinfo.ErrTransactionNotFound) {
		return "", fmt.Errorf("getting transaction: %w", err)
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting object hash: %w", err)
	}

	hooksPayload, err := git.NewHooksPayload(
		cfg,
		req.GetRepository(),
		objectHash,
		praefectTx,
		&git.UserDetails{
			UserID:   req.GetGlId(),
			Username: req.GetGlUsername(),
			Protocol: protocol,
		},
		hook,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	if err != nil {
		return "", fmt.Errorf("new hooks payload env: %w", err)
	}

	return hooksPayload, nil
}

type updateError struct {
	error
}
