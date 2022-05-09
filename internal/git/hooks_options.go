package git

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

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
func WithRefTxHook(repo repository.GitRepo) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		if repo == nil {
			return fmt.Errorf("missing repo: %w", ErrInvalidArg)
		}

		// The reference-transaction hook does not need any project-specific information
		// about the repository. So in order to make the hook usable by sites which do not
		// have a project repository available (e.g. object pools), this function accepts a
		// `repository.GitRepo` and just creates an ad-hoc proto repo.
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
func WithPackObjectsHookEnv(repo *gitalypb.Repository) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		if !cfg.PackObjectsCache.Enabled {
			return nil
		}

		if repo == nil {
			return fmt.Errorf("missing repo: %w", ErrInvalidArg)
		}

		if err := cc.configureHooks(ctx, repo, cfg, gitCmdFactory, nil, PackObjectsHook); err != nil {
			return fmt.Errorf("pack-objects hook configuration: %w", err)
		}

		cc.globals = append(cc.globals, ConfigPair{
			Key:   "uploadpack.packObjectsHook",
			Value: filepath.Join(cfg.BinDir, "gitaly-hooks"),
		})

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
	receiveHooksPayload *ReceiveHooksPayload,
	requestedHooks Hook,
) error {
	if cc.hooksConfigured {
		return errors.New("hooks already configured")
	}

	var transaction *txinfo.Transaction
	if tx, err := txinfo.TransactionFromContext(ctx); err == nil {
		transaction = &tx
	} else if !errors.Is(err, txinfo.ErrTransactionNotFound) {
		return err
	}

	payload, err := NewHooksPayload(cfg, repo, transaction, receiveHooksPayload, requestedHooks, featureflag.RawFromContext(ctx)).Env()
	if err != nil {
		return err
	}

	cc.env = append(
		cc.env,
		payload,
		fmt.Sprintf("%s=%s", log.GitalyLogDirEnvKey, cfg.Logging.Dir),
	)

	cc.globals = append(cc.globals, ConfigPair{Key: "core.hooksPath", Value: gitCmdFactory.HooksPath(ctx)})
	cc.hooksConfigured = true

	return nil
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
// variables necessary to properly execute the pre-receive, update and post-receive hooks for
// git-receive-pack(1).
func WithReceivePackHooks(req ReceivePackRequest, protocol string) CmdOpt {
	return func(ctx context.Context, cfg config.Cfg, gitCmdFactory CommandFactory, cc *cmdCfg) error {
		if err := cc.configureHooks(ctx, req.GetRepository(), cfg, gitCmdFactory, &ReceiveHooksPayload{
			UserID:   req.GetGlId(),
			Username: req.GetGlUsername(),
			Protocol: protocol,
		}, ReceivePackHooks); err != nil {
			return err
		}

		return nil
	}
}
