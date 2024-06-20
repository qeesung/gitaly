package git

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	labkittracing "gitlab.com/gitlab-org/labkit/tracing"
)

// envInjector is responsible for injecting environment variables required for tracing into
// the child process.
var envInjector = labkittracing.NewEnvInjector()

// WithDisabledHooks returns an option that satisfies the requirement to set up
// hooks, but won't in fact set up hook execution.
func WithDisabledHooks() CmdOpt {
	return func(_ context.Context, _ config.Cfg, _ CommandFactory, cc *cmdCfg) error {
		cc.hooksConfigured = true
		return nil
	}
}

// WithRefTxHook returns an option that populates the safe command with the
// environment variables necessary to properly execute a reference hook for
// repository changes that may possibly update references
func WithRefTxHook(repo storage.Repository) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		if repo == nil {
			return fmt.Errorf("missing repo: %w", ErrInvalidArg)
		}

		// The reference-transaction hook does not need any project-specific information
		// about the repository. So in order to make the hook usable by sites which do not
		// have a project repository available (e.g. object pools), this function accepts a
		// `storage.Repository` and just creates an ad-hoc proto repo.
		if err := cc.configureHooks(ctx, &gitalypb.Repository{
			StorageName:                   repo.GetStorageName(),
			GitAlternateObjectDirectories: repo.GetGitAlternateObjectDirectories(),
			GitObjectDirectory:            repo.GetGitObjectDirectory(),
			RelativePath:                  repo.GetRelativePath(),
		}, cfg, gitCmdFactory, nil, ReferenceTransactionHook); err != nil {
			return fmt.Errorf("ref hook env var: %w", err)
		}

		return nil
	}
}

// WithPackObjectsHookEnv provides metadata for gitaly-hooks so it can act as a pack-objects hook.
func WithPackObjectsHookEnv(repo *gitalypb.Repository, protocol string) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		if !cfg.PackObjectsCache.Enabled {
			return nil
		}

		if repo == nil {
			return fmt.Errorf("missing repo: %w", ErrInvalidArg)
		}

		userDetails := &UserDetails{
			Protocol: protocol,
			UserID:   metadata.GetValue(ctx, "user_id"),
			Username: metadata.GetValue(ctx, "username"),
			RemoteIP: metadata.GetValue(ctx, "remote_ip"),
		}

		if err := cc.configureHooks(
			ctx,
			repo,
			cfg,
			gitCmdFactory,
			userDetails,
			PackObjectsHook,
		); err != nil {
			return fmt.Errorf("pack-objects hook configuration: %w", err)
		}

		cc.globals = append(cc.globals, ConfigPair{
			Key:   "uploadpack.packObjectsHook",
			Value: cfg.BinaryPath("gitaly-hooks"),
		})

		return nil
	}
}

// ReceivePackRequest abstracts away the different requests that end up
// spawning git-receive-pack.
type ReceivePackRequest interface {
	GetGlId() string
	GetGlUsername() string
	GetGlRepository() string
	GetRepository() *gitalypb.Repository
}

// WithReceivePackHooks returns an option that populates the safe command with the environment
// variables necessary to properly execute the pre-receive, update, post-receive, and proc-receive
// (if enabled) hooks for git-receive-pack(1).
func WithReceivePackHooks(req ReceivePackRequest, protocol string, enableProcReceive bool) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		requestedHooks := ReceivePackHooks
		if enableProcReceive {
			// When transactions are in use, Git should only invoke pre-receive and proc-receive.
			// Our proc-receive implementation handles invoking the later hooks.
			requestedHooks = PreReceiveHook | ProcReceiveHook
		}

		if err := cc.configureHooks(ctx, req.GetRepository(), cfg, gitCmdFactory, &UserDetails{
			UserID:   req.GetGlId(),
			Username: req.GetGlUsername(),
			Protocol: protocol,
		}, requestedHooks); err != nil {
			return err
		}

		return nil
	}
}

// configureHooks updates the command configuration to include all environment
// variables required by the reference transaction hook and any other needed
// options to successfully execute hooks.
func (cc *cmdCfg) configureHooks(
	ctx context.Context,
	repo *gitalypb.Repository,
	cfg config.Cfg,
	gitCmdFactory CommandFactory,
	userDetails *UserDetails,
	requestedHooks Hook,
) error {
	if cc.hooksConfigured {
		return errors.New("hooks already configured")
	}

	objectHash, err := DetectObjectHash(ctx, gitCmdFactory, repo)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	var transaction *txinfo.Transaction
	if tx, err := txinfo.TransactionFromContext(ctx); err == nil {
		transaction = &tx
	} else if !errors.Is(err, txinfo.ErrTransactionNotFound) {
		return err
	}

	if requestedHooks&ProcReceiveHook != 0 {
		cc.globals = append(cc.globals, ConfigPair{Key: "receive.procReceiveRefs", Value: "refs"})
	}

	payload, err := NewHooksPayload(
		cfg,
		repo,
		objectHash,
		transaction,
		userDetails,
		requestedHooks,
		featureflag.FromContext(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	if err != nil {
		return err
	}

	cc.env = append(
		cc.env,
		payload,
		fmt.Sprintf("%s=%s", log.GitalyLogDirEnvKey, cfg.Logging.Dir),
	)
	cc.env = envInjector(ctx, cc.env)

	cc.globals = append(cc.globals, ConfigPair{Key: "core.hooksPath", Value: gitCmdFactory.HooksPath(ctx)})
	cc.hooksConfigured = true

	return nil
}
