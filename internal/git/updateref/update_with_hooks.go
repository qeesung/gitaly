package updateref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// UpdaterWithHooks updates a ref with Git hooks.
type UpdaterWithHooks struct {
	cfg           config.Cfg
	locator       storage.Locator
	hookManager   hook.Manager
	gitCmdFactory git.CommandFactory
	catfileCache  catfile.Cache
}

// CustomHookError contains an error message when executing a custom hook.
type CustomHookError struct {
	err      error
	hookType git.Hook
	stdout   string
	stderr   string
}

// Error returns an error message.
func (e CustomHookError) Error() string {
	// If custom hooks write the "GitLab: " or "GL-HOOK-ERR: " prefix to either their stderr or
	// their stdout, then this prefix is taken as a hint by Rails to print the error as-is in
	// the web interface. We must thus make sure to not modify these custom hook error messages
	// at all. Ideally, this logic would be handled by the hook package, which would return an
	// error struct containing all necessary information. But the hook package does not manage
	// neither stderr nor stdout itself, and these may be directly connected to the user on a
	// clone. Given that we do use byte buffers here though, we do have enough information in
	// the updateref package to handle these custom hook errors.
	//
	// Eventually, we should find a solution which allows us to bubble up the error in hook
	// package such that we can also make proper use of structured errors for custom hooks.
	if len(strings.TrimSpace(e.stderr)) > 0 {
		return e.stderr
	}
	if len(strings.TrimSpace(e.stdout)) > 0 {
		return e.stdout
	}

	return e.err.Error()
}

// Proto returns the Protobuf representation of this error.
func (e CustomHookError) Proto() *gitalypb.CustomHookError {
	hookType := gitalypb.CustomHookError_HOOK_TYPE_UNSPECIFIED
	switch e.hookType {
	case git.PreReceiveHook:
		hookType = gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE
	case git.UpdateHook:
		hookType = gitalypb.CustomHookError_HOOK_TYPE_UPDATE
	case git.PostReceiveHook:
		hookType = gitalypb.CustomHookError_HOOK_TYPE_POSTRECEIVE
	}

	return &gitalypb.CustomHookError{
		HookType: hookType,
		Stdout:   []byte(e.stdout),
		Stderr:   []byte(e.stderr),
	}
}

// Unwrap will return the embedded error.
func (e CustomHookError) Unwrap() error {
	return e.err
}

// wrapHookError wraps errors returned by the hook manager into either a CustomHookError if it
// returned a `hook.CustomHookError`, or alternatively return the error with stderr or stdout
// appended to the message.
func wrapHookError(err error, hookType git.Hook, stdout, stderr string) error {
	var customHookErr hook.CustomHookError
	if errors.As(err, &customHookErr) {
		return CustomHookError{
			err:      err,
			hookType: hookType,
			stdout:   stdout,
			stderr:   stderr,
		}
	}

	if len(strings.TrimSpace(stderr)) > 0 {
		return fmt.Errorf("%w, stderr: %q", err, stderr)
	}
	if len(strings.TrimSpace(stdout)) > 0 {
		return fmt.Errorf("%w, stdout: %q", err, stdout)
	}

	return err
}

// Error reports an error in git update-ref
type Error struct {
	// Reference is the name of the reference that would have been updated.
	Reference git.ReferenceName
	// OldOID and NewOID are the expected object IDs previous to and after the update if it
	// would have succeeded.
	OldOID, NewOID git.ObjectID
}

func (e Error) Error() string {
	return fmt.Sprintf("Could not update %s. Please refresh and try again.", e.Reference)
}

// NewUpdaterWithHooks creates a new instance of a struct that will update a Git reference.
func NewUpdaterWithHooks(
	cfg config.Cfg,
	locator storage.Locator,
	hookManager hook.Manager,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
) *UpdaterWithHooks {
	return &UpdaterWithHooks{
		cfg:           cfg,
		locator:       locator,
		hookManager:   hookManager,
		gitCmdFactory: gitCmdFactory,
		catfileCache:  catfileCache,
	}
}

// UpdateReference updates a branch with a given commit ID using the Git hooks. If a quarantine
// directory is given, then the pre-receive, update and reference-transaction hook will be invoked
// with the quarantined repository as returned by the quarantine structure. If these hooks succeed,
// quarantined objects will be migrated and all subsequent hooks are executed via the unquarantined
// repository.
func (u *UpdaterWithHooks) UpdateReference(
	ctx context.Context,
	repoProto *gitalypb.Repository,
	user *gitalypb.User,
	quarantineDir *quarantine.Dir,
	reference git.ReferenceName,
	newrev, oldrev git.ObjectID,
	pushOptions ...string,
) error {
	var transaction *txinfo.Transaction
	if tx, err := txinfo.TransactionFromContext(ctx); err == nil {
		transaction = &tx
	} else if !errors.Is(err, txinfo.ErrTransactionNotFound) {
		return fmt.Errorf("getting transaction: %w", err)
	}

	repo := u.localrepo(repoProto)

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	if reference == "" {
		return fmt.Errorf("reference cannot be empty")
	}
	if err := objectHash.ValidateHex(oldrev.String()); err != nil {
		return fmt.Errorf("validating old value: %w", err)
	}
	if err := objectHash.ValidateHex(newrev.String()); err != nil {
		return fmt.Errorf("validating new value: %w", err)
	}

	changes := fmt.Sprintf("%s %s %s\n", oldrev, newrev, reference)

	receiveHooksPayload := git.UserDetails{
		UserID:   user.GetGlId(),
		Username: user.GetGlUsername(),
		Protocol: "web",
	}

	// In case there's no quarantine directory, we simply take the normal unquarantined
	// repository as input for the hooks payload. Otherwise, we'll take the quarantined
	// repository, which carries information about the quarantined object directory. This is
	// then subsequently passed to Rails, which can use the quarantine directory to more
	// efficiently query which objects are new.
	quarantinedRepo := repoProto
	if quarantineDir != nil {
		quarantinedRepo = quarantineDir.QuarantinedRepo()
	}

	hooksPayload, err := git.NewHooksPayload(u.cfg, quarantinedRepo, transaction, &receiveHooksPayload, git.ReceivePackHooks, featureflag.FromContext(ctx)).Env()
	if err != nil {
		return fmt.Errorf("constructing hooks payload: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := u.hookManager.PreReceiveHook(ctx, quarantinedRepo, pushOptions, []string{hooksPayload}, strings.NewReader(changes), &stdout, &stderr); err != nil {
		return fmt.Errorf("running pre-receive hooks: %w", wrapHookError(err, git.PreReceiveHook, stdout.String(), stderr.String()))
	}

	// Now that Rails has told us that the change is okay via the pre-receive hook, we can
	// migrate any potentially quarantined objects into the main repository. This must happen
	// before we start updating the refs because git-update-ref(1) will verify that it got all
	// referenced objects available.
	if quarantineDir != nil {
		if err := quarantineDir.Migrate(); err != nil {
			return fmt.Errorf("migrating quarantined objects: %w", err)
		}

		// We only need to update the hooks payload to the unquarantined repo in case we
		// had a quarantine environment. Otherwise, the initial hooks payload is for the
		// real repository anyway.
		hooksPayload, err = git.NewHooksPayload(u.cfg, repoProto, transaction, &receiveHooksPayload, git.ReceivePackHooks, featureflag.FromContext(ctx)).Env()
		if err != nil {
			return fmt.Errorf("constructing quarantined hooks payload: %w", err)
		}
	}

	if err := u.hookManager.UpdateHook(ctx, quarantinedRepo, reference.String(), oldrev.String(), newrev.String(), []string{hooksPayload}, &stdout, &stderr); err != nil {
		return fmt.Errorf("running update hooks: %w", wrapHookError(err, git.UpdateHook, stdout.String(), stderr.String()))
	}

	// We are already manually invoking the reference-transaction hook, so there is no need to
	// set up hooks again here. One could argue that it would be easier to just have git handle
	// execution of the reference-transaction hook. But unfortunately, it has proven to be
	// problematic: if we queue a deletion, and the reference to be deleted exists both as
	// packed-ref and as loose ref, then we would see two transactions: first a transaction
	// deleting the packed-ref which would otherwise get unshadowed by deleting the loose ref,
	// and only then do we see the deletion of the loose ref. So this depends on how well a repo
	// is packed, which is obviously a bad thing as Gitaly nodes may be differently packed. We
	// thus continue to manually drive the reference-transaction hook here, which doesn't have
	// this problem.
	updater, err := New(ctx, repo, WithDisabledTransactions())
	if err != nil {
		return fmt.Errorf("creating updater: %w", err)
	}

	if err := updater.Update(reference, newrev, oldrev); err != nil {
		return fmt.Errorf("queueing ref update: %w", err)
	}

	// We need to lock the reference before executing the reference-transaction hook such that
	// there cannot be any concurrent modification.
	if err := updater.Prepare(); err != nil {
		return Error{
			Reference: reference,
			OldOID:    oldrev,
			NewOID:    newrev,
		}
	}
	// We need to explicitly cancel the update here such that we release the lock when this
	// function exits if there is any error between locking and committing.
	defer func() { _ = updater.Cancel() }()

	if err := u.hookManager.ReferenceTransactionHook(ctx, hook.ReferenceTransactionPrepared, []string{hooksPayload}, strings.NewReader(changes)); err != nil {
		return fmt.Errorf("executing preparatory reference-transaction hook: %w", err)
	}

	if err := updater.Commit(); err != nil {
		return Error{
			Reference: reference,
			OldOID:    oldrev,
			NewOID:    newrev,
		}
	}

	if err := u.hookManager.ReferenceTransactionHook(ctx, hook.ReferenceTransactionCommitted, []string{hooksPayload}, strings.NewReader(changes)); err != nil {
		return fmt.Errorf("executing committing reference-transaction hook: %w", err)
	}

	if err := u.hookManager.PostReceiveHook(ctx, repoProto, pushOptions, []string{hooksPayload}, strings.NewReader(changes), &stdout, &stderr); err != nil {
		// CustomHook errors are returned in case a custom hook has returned an error code.
		// The post-receive hook has special semantics though. Quoting githooks(5):
		//
		//    This hook does not affect the outcome of git receive-pack, as it is called
		//    after the real work is done.
		//
		// This means that even if the hook returns an error, then that error should not
		// impact whatever git-receive-pack(1) has been doing. And given that we emulate
		// behaviour of this command here, we need to behave the same.
		var customHookErr hook.CustomHookError
		if errors.As(err, &customHookErr) {
			// Only log the error when we've got a custom-hook error, but otherwise
			// ignore it and continue with whatever we have been doing.
			ctxlogrus.Extract(ctx).WithError(err).Error("custom post-receive hook returned an error")
		} else {
			return fmt.Errorf("running post-receive hooks: %w", wrapHookError(err, git.PostReceiveHook, stdout.String(), stderr.String()))
		}
	}

	return nil
}

func (u *UpdaterWithHooks) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(u.locator, u.gitCmdFactory, u.catfileCache, repo)
}
