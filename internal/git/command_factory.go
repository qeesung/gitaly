package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v14/internal/command"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/log"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata/featureflag"
	"golang.org/x/sys/unix"
)

var globalOptions = []GlobalOption{
	// Synchronize object files to lessen the likelihood of
	// repository corruption in case the server crashes.
	ConfigPair{Key: "core.fsyncObjectFiles", Value: "true"},

	// Disable automatic garbage collection as we handle scheduling
	// of it ourselves.
	ConfigPair{Key: "gc.auto", Value: "0"},

	// CRLF line endings will get replaced with LF line endings
	// when writing blobs to the object database. No conversion is
	// done when reading blobs from the object database. This is
	// required for the web editor.
	ConfigPair{Key: "core.autocrlf", Value: "input"},

	// Git allows the use of replace refs, where a given object ID can be replaced with a
	// different one. The result is that Git commands would use the new object instead of the
	// old one in almost all contexts. This is a security threat: an adversary may use this
	// mechanism to replace malicious commits with seemingly benign ones. We thus globally
	// disable this mechanism.
	ConfigPair{Key: "core.useReplaceRefs", Value: "false"},
}

// ExecutionEnvironment describes the environment required to execute a Git command
type ExecutionEnvironment struct {
	// BinaryPath is the path to the Git binary.
	BinaryPath string
	// EnvironmentVariables are variables which must be set when running the Git binary.
	EnvironmentVariables []string
}

// CommandFactory is designed to create and run git commands in a protected and fully managed manner.
type CommandFactory interface {
	// New creates a new command for the repo repository.
	New(ctx context.Context, repo repository.GitRepo, sc Cmd, opts ...CmdOpt) (*command.Command, error)
	// NewWithoutRepo creates a command without a target repository.
	NewWithoutRepo(ctx context.Context, sc Cmd, opts ...CmdOpt) (*command.Command, error)
	// NewWithDir creates a command without a target repository that would be executed in dir directory.
	NewWithDir(ctx context.Context, dir string, sc Cmd, opts ...CmdOpt) (*command.Command, error)
	// GetExecutionEnvironment returns parameters required to execute Git commands.
	GetExecutionEnvironment(context.Context) ExecutionEnvironment
	// HooksPath returns the path where Gitaly's Git hooks reside.
	HooksPath(context.Context) string
	// GitVersion returns the Git version used by the command factory.
	GitVersion(context.Context) (Version, error)
}

type execCommandFactoryConfig struct {
	hooksPath string
}

// ExecCommandFactoryOption is an option that can be passed to NewExecCommandFactory.
type ExecCommandFactoryOption func(*execCommandFactoryConfig)

// WithSkipHooks will skip any use of hooks in this command factory.
func WithSkipHooks() ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.hooksPath = "/var/empty"
	}
}

// WithHooksPath will override the path where hooks are to be found.
func WithHooksPath(hooksPath string) ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.hooksPath = hooksPath
	}
}

type hookDirectories struct {
	rubyHooksPath string
	tempHooksPath string
}

// ExecCommandFactory knows how to properly construct different types of commands.
type ExecCommandFactory struct {
	locator               storage.Locator
	cfg                   config.Cfg
	cgroupsManager        cgroups.Manager
	invalidCommandsMetric *prometheus.CounterVec
	hookDirs              hookDirectories

	cachedGitVersionLock sync.RWMutex
	cachedGitVersion     Version
	cachedGitStat        *os.FileInfo
}

// NewExecCommandFactory returns a new instance of initialized ExecCommandFactory. The returned
// cleanup function shall be executed when the server shuts down.
func NewExecCommandFactory(cfg config.Cfg, opts ...ExecCommandFactoryOption) (_ *ExecCommandFactory, _ func(), returnedErr error) {
	var factoryCfg execCommandFactoryConfig
	for _, opt := range opts {
		opt(&factoryCfg)
	}

	hookDirectories, cleanup, err := setupHookDirectories(cfg, factoryCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up hooks: %w", err)
	}
	defer func() {
		if returnedErr != nil {
			cleanup()
		}
	}()

	gitCmdFactory := &ExecCommandFactory{
		cfg:            cfg,
		locator:        config.NewLocator(cfg),
		cgroupsManager: cgroups.NewManager(cfg.Cgroups),
		invalidCommandsMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_invalid_commands_total",
				Help: "Total number of invalid arguments tried to execute",
			},
			[]string{"command"},
		),
		hookDirs: hookDirectories,
	}

	return gitCmdFactory, cleanup, nil
}

// Describe is used to describe Prometheus metrics.
func (cf *ExecCommandFactory) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(cf, descs)
}

// Collect is used to collect Prometheus metrics.
func (cf *ExecCommandFactory) Collect(metrics chan<- prometheus.Metric) {
	cf.invalidCommandsMetric.Collect(metrics)
	cf.cgroupsManager.Collect(metrics)
}

// New creates a new command for the repo repository.
func (cf *ExecCommandFactory) New(ctx context.Context, repo repository.GitRepo, sc Cmd, opts ...CmdOpt) (*command.Command, error) {
	return cf.newCommand(ctx, repo, "", sc, opts...)
}

// NewWithoutRepo creates a command without a target repository.
func (cf *ExecCommandFactory) NewWithoutRepo(ctx context.Context, sc Cmd, opts ...CmdOpt) (*command.Command, error) {
	return cf.newCommand(ctx, nil, "", sc, opts...)
}

// NewWithDir creates a new command.Command whose working directory is set
// to dir. Arguments are validated before the command is being run. It is
// invalid to use an empty directory.
func (cf *ExecCommandFactory) NewWithDir(ctx context.Context, dir string, sc Cmd, opts ...CmdOpt) (*command.Command, error) {
	if dir == "" {
		return nil, errors.New("no 'dir' provided")
	}

	return cf.newCommand(ctx, nil, dir, sc, opts...)
}

// GetExecutionEnvironment returns parameters required to execute Git commands.
func (cf *ExecCommandFactory) GetExecutionEnvironment(context.Context) ExecutionEnvironment {
	return ExecutionEnvironment{
		BinaryPath:           cf.cfg.Git.BinPath,
		EnvironmentVariables: cf.cfg.GitExecEnv(),
	}
}

// HooksPath returns the path where Gitaly's Git hooks reside.
func (cf *ExecCommandFactory) HooksPath(ctx context.Context) string {
	if featureflag.HooksInTempdir.IsEnabled(ctx) {
		return cf.hookDirs.tempHooksPath
	}
	return cf.hookDirs.rubyHooksPath
}

func setupHookDirectories(cfg config.Cfg, factoryCfg execCommandFactoryConfig) (hookDirectories, func(), error) {
	if factoryCfg.hooksPath != "" {
		return hookDirectories{
			rubyHooksPath: factoryCfg.hooksPath,
			tempHooksPath: factoryCfg.hooksPath,
		}, func() {}, nil
	}

	// The old and now-deprecated location of Gitaly's hooks is in the Ruby directory.
	rubyHooksPath := filepath.Join(cfg.Ruby.Dir, "git-hooks")

	var errs []string
	for _, hookName := range []string{"pre-receive", "post-receive", "update"} {
		hookPath := filepath.Join(rubyHooksPath, hookName)
		if err := unix.Access(hookPath, unix.X_OK); err != nil {
			if errors.Is(err, os.ErrPermission) {
				errs = append(errs, fmt.Sprintf("not executable: %v", hookPath))
			} else {
				errs = append(errs, err.Error())
			}
		}
	}

	if len(errs) > 0 {
		return hookDirectories{}, nil, fmt.Errorf(strings.Join(errs, ", "))
	}

	if cfg.BinDir == "" {
		return hookDirectories{}, nil, errors.New("binary directory required to set up hooks")
	}

	// This sets up the new hook location. Hooks now live in a temporary directory, where all
	// hooks are symlinks to the `gitaly-hooks` binary.
	tempHooksPath, err := os.MkdirTemp("", "gitaly-hooks-")
	if err != nil {
		return hookDirectories{}, nil, fmt.Errorf("creating temporary hooks directory: %w", err)
	}

	gitalyHooksPath := filepath.Join(cfg.BinDir, "gitaly-hooks")

	// And now we symlink all required hooks to the wrapper script.
	for _, hook := range []string{"pre-receive", "post-receive", "update", "reference-transaction"} {
		if err := os.Symlink(gitalyHooksPath, filepath.Join(tempHooksPath, hook)); err != nil {
			return hookDirectories{}, nil, fmt.Errorf("creating symlink for %s hook: %w", hook, err)
		}
	}

	return hookDirectories{
			rubyHooksPath: rubyHooksPath,
			tempHooksPath: tempHooksPath,
		}, func() {
			if err := os.RemoveAll(tempHooksPath); err != nil {
				log.Default().WithError(err).Error("cleaning up temporary hooks path")
			}
		}, nil
}

func statDiffers(a, b os.FileInfo) bool {
	return a.Size() != b.Size() || a.ModTime() != b.ModTime() || a.Mode() != b.Mode()
}

// GitVersion returns the Git version in use. The version is cached as long as the binary remains
// unchanged as determined by stat(3P).
func (cf *ExecCommandFactory) GitVersion(ctx context.Context) (Version, error) {
	stat, err := os.Stat(cf.GetExecutionEnvironment(ctx).BinaryPath)
	if err != nil {
		return Version{}, fmt.Errorf("cannot stat Git binary: %w", err)
	}

	cf.cachedGitVersionLock.RLock()
	upToDate := cf.cachedGitStat != nil && !statDiffers(stat, *cf.cachedGitStat)
	cachedGitVersion := cf.cachedGitVersion
	cf.cachedGitVersionLock.RUnlock()

	if upToDate {
		return cachedGitVersion, nil
	}

	cf.cachedGitVersionLock.Lock()
	defer cf.cachedGitVersionLock.Unlock()

	// We cannot reuse the stat(3P) information from above given that it wasn't acquired under
	// the write-lock. As such, it may have been invalidated by a concurrent thread which has
	// already updated the Git version information.
	stat, err = os.Stat(cf.GetExecutionEnvironment(ctx).BinaryPath)
	if err != nil {
		return Version{}, fmt.Errorf("cannot stat Git binary: %w", err)
	}

	// There is a race here: if the Git executable has changed between calling stat(3P) on the
	// binary and executing it, then we may report the wrong Git version. This race is inherent
	// though: it can also happen after `GitVersion()` was called, so it doesn't really help to
	// retry version detection here. Instead, we just live with this raciness -- the next call
	// to `GitVersion()` would detect the version being out-of-date anyway and thus correct it.
	cmd, err := cf.NewWithoutRepo(ctx, SubCmd{
		Name: "version",
	})
	if err != nil {
		return Version{}, fmt.Errorf("spawning version command: %w", err)
	}

	gitVersion, err := parseVersionFromCommand(cmd)
	if err != nil {
		return Version{}, err
	}

	cf.cachedGitVersion = gitVersion
	cf.cachedGitStat = &stat

	return gitVersion, nil
}

// newCommand creates a new command.Command for the given git command. If a repo is given, then the
// command will be run in the context of that repository. Note that this sets up arguments and
// environment variables for git, but doesn't run in the directory itself. If a directory
// is given, then the command will be run in that directory.
func (cf *ExecCommandFactory) newCommand(ctx context.Context, repo repository.GitRepo, dir string, sc Cmd, opts ...CmdOpt) (*command.Command, error) {
	config, err := cf.combineOpts(ctx, sc, opts)
	if err != nil {
		return nil, err
	}

	args, err := cf.combineArgs(ctx, cf.cfg.Git.Config, sc, config)
	if err != nil {
		return nil, err
	}

	env := config.env

	if repo != nil {
		repoPath, err := cf.locator.GetRepoPath(repo)
		if err != nil {
			return nil, err
		}

		env = append(alternates.Env(repoPath, repo.GetGitObjectDirectory(), repo.GetGitAlternateObjectDirectories()), env...)
		args = append([]string{"--git-dir", repoPath}, args...)
	}

	execEnv := cf.GetExecutionEnvironment(ctx)

	env = append(env, command.GitEnv...)
	env = append(env, execEnv.EnvironmentVariables...)

	execCommand := exec.Command(execEnv.BinaryPath, args...)
	execCommand.Dir = dir

	command, err := command.New(ctx, execCommand, config.stdin, config.stdout, config.stderr, env...)
	if err != nil {
		return nil, err
	}

	if featureflag.RunCommandsInCGroup.IsEnabled(ctx) {
		if err := cf.cgroupsManager.AddCommand(command); err != nil {
			return nil, err
		}
	}

	return command, nil
}

func (cf *ExecCommandFactory) combineOpts(ctx context.Context, sc Cmd, opts []CmdOpt) (cmdCfg, error) {
	var config cmdCfg

	commandDescription, ok := commandDescriptions[sc.Subcommand()]
	if !ok {
		return cmdCfg{}, fmt.Errorf("invalid sub command name %q: %w", sc.Subcommand(), ErrInvalidArg)
	}

	for _, opt := range opts {
		if err := opt(ctx, cf.cfg, cf, &config); err != nil {
			return cmdCfg{}, err
		}
	}

	if !config.hooksConfigured && commandDescription.mayUpdateRef() {
		return cmdCfg{}, fmt.Errorf("subcommand %q: %w", sc.Subcommand(), ErrHookPayloadRequired)
	}

	return config, nil
}

func (cf *ExecCommandFactory) combineArgs(ctx context.Context, gitConfig []config.GitConfig, sc Cmd, cc cmdCfg) (_ []string, err error) {
	var args []string

	defer func() {
		if err != nil && IsInvalidArgErr(err) && len(args) > 0 {
			cf.invalidCommandsMetric.WithLabelValues(sc.Subcommand()).Inc()
		}
	}()

	commandDescription, ok := commandDescriptions[sc.Subcommand()]
	if !ok {
		return nil, fmt.Errorf("invalid sub command name %q: %w", sc.Subcommand(), ErrInvalidArg)
	}

	commandSpecificOptions := commandDescription.opts
	if commandDescription.mayGeneratePackfiles() {
		commandSpecificOptions = append(commandSpecificOptions,
			ConfigPair{Key: "pack.windowMemory", Value: "100m"},
			ConfigPair{Key: "pack.writeReverseIndex", Value: "true"},
		)
	}

	// As global options may cancel out each other, we have a clearly defined order in which
	// globals get applied. The order is similar to how git handles configuration options from
	// most general to most specific. This allows callsites to override options which would
	// otherwise be set up automatically. The exception to this is configuration specified by
	// the admin, which always overrides all other items. The following order of precedence
	// applies:
	//
	// 1. Globals which get set up by default for all git commands.
	// 2. Globals which get set up by default for a given git command.
	// 3. Globals passed via command options, e.g. as set up by
	//    `WithReftxHook()`.
	// 4. Configuration as provided by the admin in Gitaly's config.toml.
	var combinedGlobals []GlobalOption
	combinedGlobals = append(combinedGlobals, globalOptions...)
	combinedGlobals = append(combinedGlobals, commandSpecificOptions...)
	combinedGlobals = append(combinedGlobals, cc.globals...)
	for _, configPair := range gitConfig {
		combinedGlobals = append(combinedGlobals, ConfigPair{
			Key:   configPair.Key,
			Value: configPair.Value,
		})
	}

	for _, global := range combinedGlobals {
		globalArgs, err := global.GlobalArgs()
		if err != nil {
			return nil, err
		}
		args = append(args, globalArgs...)
	}

	scArgs, err := sc.CommandArgs()
	if err != nil {
		return nil, err
	}

	return append(args, scArgs...), nil
}
