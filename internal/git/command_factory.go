package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2hooks"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/labkit/correlation"
	"golang.org/x/time/rate"
)

const (
	// BigFileThresholdMB is the threshold we configure via `core.bigFileThreshold` and determines the maximum size
	// after which Git considers files to be big. Please refer to `GlobalConfiguration()` for more details.
	BigFileThresholdMB = 50
	// maxTrace2EventPerSecond is the maximum number of events that can be processed per second
	maxTrace2EventPerSecond = 40
	// Rate limiter is immediately allocated the maxBurstToken value. Burst is the maximum number of tokens
	// that can be consumed in a single call
	maxBurstToken = 40
)

// CommandFactory is designed to create and run git commands in a protected and fully managed manner.
type CommandFactory interface {
	// New creates a new command for the repo repository.
	New(ctx context.Context, repo storage.Repository, sc Command, opts ...CmdOpt) (*command.Command, error)
	// NewWithoutRepo creates a command without a target repository.
	NewWithoutRepo(ctx context.Context, sc Command, opts ...CmdOpt) (*command.Command, error)
	// GetExecutionEnvironment returns parameters required to execute Git commands.
	GetExecutionEnvironment(context.Context) ExecutionEnvironment
	// HooksPath returns the path where Gitaly's Git hooks reside.
	HooksPath(context.Context) string
	// GitVersion returns the Git version used by the command factory.
	GitVersion(context.Context) (Version, error)
}

type execCommandFactoryConfig struct {
	hooksPath           string
	gitBinaryPath       string
	cgroupsManager      cgroups.Manager
	trace2Hooks         []trace2.Hook
	traceRateLimiter    *rate.Limiter
	execEnvConstructors []ExecutionEnvironmentConstructor
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

// WithGitBinaryPath overrides the path to the Git binary that shall be executed.
func WithGitBinaryPath(path string) ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.gitBinaryPath = path
	}
}

// WithCgroupsManager overrides the Cgroups manager used by the command factory.
func WithCgroupsManager(cgroupsManager cgroups.Manager) ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.cgroupsManager = cgroupsManager
	}
}

// WithTrace2Hooks overrides default trace2 hooks used by trace2 manager
func WithTrace2Hooks(hooks []trace2.Hook) ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.trace2Hooks = hooks
	}
}

// DefaultTrace2HooksFor creates a list of all Trace2 hooks. It doesn't mean all hooks are triggered.
// Each hook's activation status will be evaluated before the command starts.
func DefaultTrace2HooksFor(ctx context.Context, subCmd string, logger log.Logger, rl *rate.Limiter) []trace2.Hook {
	var hooks []trace2.Hook
	if tracing.IsSampled(ctx) {
		hooks = append(hooks, trace2hooks.NewTracingExporter())
	}
	if subCmd == "pack-objects" {
		hooks = append(hooks, trace2hooks.NewPackObjectsMetrics())
	}
	if featureflag.LogGitTraces.IsEnabled(ctx) {
		hooks = append(hooks, trace2hooks.NewLogExporter(rl, logger))
	}
	return hooks
}

// WithExecutionEnvironmentConstructors overrides the default Git execution environments used by the
// command factory.
func WithExecutionEnvironmentConstructors(constructors ...ExecutionEnvironmentConstructor) ExecCommandFactoryOption {
	return func(cfg *execCommandFactoryConfig) {
		cfg.execEnvConstructors = constructors
	}
}

type hookDirectories struct {
	tempHooksPath string
}

type cachedGitVersion struct {
	version Version
	stat    os.FileInfo
}

// ExecCommandFactory knows how to properly construct different types of commands.
type ExecCommandFactory struct {
	locator               storage.Locator
	cfg                   config.Cfg
	execEnvs              []ExecutionEnvironment
	logger                log.Logger
	cgroupsManager        cgroups.Manager
	trace2Hooks           []trace2.Hook
	traceRateLimiter      *rate.Limiter
	invalidCommandsMetric *prometheus.CounterVec
	hookDirs              hookDirectories

	cachedGitVersionLock     sync.RWMutex
	cachedGitVersionByBinary map[string]cachedGitVersion
}

// NewExecCommandFactory returns a new instance of initialized ExecCommandFactory. The returned
// cleanup function shall be executed when the server shuts down.
func NewExecCommandFactory(cfg config.Cfg, logger log.Logger, opts ...ExecCommandFactoryOption) (_ *ExecCommandFactory, _ func(), returnedErr error) {
	var factoryCfg execCommandFactoryConfig
	for _, opt := range opts {
		opt(&factoryCfg)
	}

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	defer func() {
		if returnedErr != nil {
			runCleanups()
		}
	}()

	hookDirectories, cleanup, err := setupHookDirectories(cfg, factoryCfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up hooks: %w", err)
	}
	cleanups = append(cleanups, cleanup)

	execEnvs, cleanup, err := setupGitExecutionEnvironments(cfg, factoryCfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up Git execution environment: %w", err)
	}
	cleanups = append(cleanups, cleanup)

	cgroupsManager := factoryCfg.cgroupsManager
	if cgroupsManager == nil {
		cgroupsManager = cgroups.NewManager(cfg.Cgroups, logger, os.Getpid())
	}

	traceRateLimiter := factoryCfg.traceRateLimiter
	if traceRateLimiter == nil {
		traceRateLimiter = rate.NewLimiter(maxTrace2EventPerSecond, maxBurstToken)
	}

	gitCmdFactory := &ExecCommandFactory{
		cfg:              cfg,
		execEnvs:         execEnvs,
		logger:           logger,
		locator:          config.NewLocator(cfg),
		cgroupsManager:   cgroupsManager,
		trace2Hooks:      factoryCfg.trace2Hooks,
		traceRateLimiter: traceRateLimiter,
		invalidCommandsMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_invalid_commands_total",
				Help: "Total number of invalid arguments tried to execute",
			},
			[]string{"command"},
		),
		hookDirs:                 hookDirectories,
		cachedGitVersionByBinary: make(map[string]cachedGitVersion),
	}

	return gitCmdFactory, runCleanups, nil
}

// setupGitExecutionEnvironments assembles a Git execution environment that can be used to run Git
// commands. It warns if no path was specified in the configuration.
func setupGitExecutionEnvironments(cfg config.Cfg, factoryCfg execCommandFactoryConfig, logger log.Logger) ([]ExecutionEnvironment, func(), error) {
	sharedEnvironment := []string{
		// Force English locale for consistency on output messages and to help us debug in
		// case we get bug reports from customers whose system-locale would be different.
		"LANG=en_US.UTF-8",
		// Ask Git to never prompt us for any information like e.g. credentials.
		"GIT_TERMINAL_PROMPT=0",
		// Prevent the environment from affecting git calls by ignoring the configuration files.
		// This should be done always but we have to wait until 15.0 due to backwards compatibility
		// concerns.
		//
		// See https://gitlab.com/gitlab-org/gitaly/-/issues/3617.
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"XDG_CONFIG_HOME=/dev/null",
	}

	if factoryCfg.gitBinaryPath != "" {
		return []ExecutionEnvironment{
			{BinaryPath: factoryCfg.gitBinaryPath, EnvironmentVariables: sharedEnvironment},
		}, func() {}, nil
	}

	constructors := factoryCfg.execEnvConstructors
	if factoryCfg.execEnvConstructors == nil {
		constructors = defaultExecutionEnvironmentConstructors
	}

	var execEnvs []ExecutionEnvironment
	for _, constructor := range constructors {
		execEnv, err := constructor.Construct(cfg)
		if err != nil {
			// In case the environment has not been configured by the user we simply
			// skip it.
			if errors.Is(err, ErrNotConfigured) {
				continue
			}

			// But if it has been configured and we fail to set it up then it signifies
			// a real error.
			return nil, nil, fmt.Errorf("constructing Git environment: %w", err)
		}

		execEnv.EnvironmentVariables = append(execEnv.EnvironmentVariables, sharedEnvironment...)

		execEnvs = append(execEnvs, execEnv)
	}

	if len(execEnvs) == 0 {
		execEnv, err := FallbackGitEnvironmentConstructor{}.Construct(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("could not set up any Git execution environments")
		}
		execEnv.EnvironmentVariables = append(execEnv.EnvironmentVariables, sharedEnvironment...)

		logger.WithFields(log.Fields{
			"resolvedPath": execEnv.BinaryPath,
		}).Warn("Git has not been properly configured, falling back to Git found on PATH")

		execEnvs = append(execEnvs, execEnv)
	}

	return execEnvs, func() {
		for _, execEnv := range execEnvs {
			if err := execEnv.Cleanup(); err != nil {
				logger.WithError(err).Error("execution environment cleanup failed")
			}
		}
	}, nil
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
func (cf *ExecCommandFactory) New(ctx context.Context, repo storage.Repository, sc Command, opts ...CmdOpt) (*command.Command, error) {
	return cf.newCommand(ctx, repo, sc, opts...)
}

// NewWithoutRepo creates a command without a target repository.
func (cf *ExecCommandFactory) NewWithoutRepo(ctx context.Context, sc Command, opts ...CmdOpt) (*command.Command, error) {
	return cf.newCommand(ctx, nil, sc, opts...)
}

// GetExecutionEnvironment returns parameters required to execute Git commands.
func (cf *ExecCommandFactory) GetExecutionEnvironment(ctx context.Context) ExecutionEnvironment {
	// We first go through all execution environments and check whether any of them is enabled
	// in the current context, which most importantly will check their respective feature flags.
	for _, execEnv := range cf.execEnvs {
		if execEnv.IsEnabled(ctx) {
			return execEnv
		}
	}

	// If none is enabled though, we simply use the first execution environment, which is also
	// the one with the highest priority. This can for example happen in case we only were able to
	// construct a single execution environment that is currently feature flagged.
	return cf.execEnvs[0]
}

// HooksPath returns the path where Gitaly's Git hooks reside.
func (cf *ExecCommandFactory) HooksPath(ctx context.Context) string {
	return cf.hookDirs.tempHooksPath
}

func setupHookDirectories(cfg config.Cfg, factoryCfg execCommandFactoryConfig, logger log.Logger) (hookDirectories, func(), error) {
	if factoryCfg.hooksPath != "" {
		return hookDirectories{
			tempHooksPath: factoryCfg.hooksPath,
		}, func() {}, nil
	}

	if cfg.BinDir == "" {
		return hookDirectories{}, nil, errors.New("binary directory required to set up hooks")
	}

	// This sets up the new hook location. Hooks now live in a temporary directory, where all
	// hooks are symlinks to the `gitaly-hooks` binary.
	tempHooksPath, err := os.MkdirTemp(cfg.RuntimeDir, "hooks-*.d")
	if err != nil {
		return hookDirectories{}, nil, fmt.Errorf("creating temporary hooks directory: %w", err)
	}

	// And now we symlink all required hooks to the wrapper script.
	for _, hook := range []string{"pre-receive", "post-receive", "update", "reference-transaction", "proc-receive"} {
		if err := os.Symlink(cfg.BinaryPath("gitaly-hooks"), filepath.Join(tempHooksPath, hook)); err != nil {
			return hookDirectories{}, nil, fmt.Errorf("creating symlink for %s hook: %w", hook, err)
		}
	}

	return hookDirectories{
			tempHooksPath: tempHooksPath,
		}, func() {
			if err := os.RemoveAll(tempHooksPath); err != nil {
				logger.WithError(err).Error("cleaning up temporary hooks path")
			}
		}, nil
}

func statDiffers(a, b os.FileInfo) bool {
	return a.Size() != b.Size() || a.ModTime() != b.ModTime() || a.Mode() != b.Mode()
}

// GitVersion returns the Git version in use. The version is cached as long as the binary remains
// unchanged as determined by stat(3P).
func (cf *ExecCommandFactory) GitVersion(ctx context.Context) (Version, error) {
	gitBinary := cf.GetExecutionEnvironment(ctx).BinaryPath

	stat, err := os.Stat(gitBinary)
	if err != nil {
		return Version{}, fmt.Errorf("cannot stat Git binary: %w", err)
	}

	cf.cachedGitVersionLock.RLock()
	cachedVersion, upToDate := cf.cachedGitVersionByBinary[gitBinary]
	if upToDate {
		upToDate = !statDiffers(stat, cachedVersion.stat)
	}
	cf.cachedGitVersionLock.RUnlock()

	if upToDate {
		return cachedVersion.version, nil
	}

	cf.cachedGitVersionLock.Lock()
	defer cf.cachedGitVersionLock.Unlock()

	execEnv := cf.GetExecutionEnvironment(ctx)

	// We cannot reuse the stat(3P) information from above given that it wasn't acquired under
	// the write-lock. As such, it may have been invalidated by a concurrent thread which has
	// already updated the Git version information.
	stat, err = os.Stat(execEnv.BinaryPath)
	if err != nil {
		return Version{}, fmt.Errorf("cannot stat Git binary: %w", err)
	}

	// There is a race here: if the Git executable has changed between calling stat(3P) on the
	// binary and executing it, then we may report the wrong Git version. This race is inherent
	// though: it can also happen after `GitVersion()` was called, so it doesn't really help to
	// retry version detection here. Instead, we just live with this raciness -- the next call
	// to `GitVersion()` would detect the version being out-of-date anyway and thus correct it.
	//
	// Furthermore, note that we're not using `newCommand()` but instead hand-craft the command.
	// This is required to avoid a cyclic dependency when we need to check the version in
	// `newCommand()` itself.
	var versionBuffer bytes.Buffer
	cmd, err := command.New(ctx, cf.logger, []string{execEnv.BinaryPath, "version"},
		command.WithEnvironment(execEnv.EnvironmentVariables),
		command.WithStdout(&versionBuffer),
	)
	if err != nil {
		return Version{}, fmt.Errorf("spawning version command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return Version{}, fmt.Errorf("waiting for version: %w", err)
	}

	gitVersion, err := parseVersionOutput(versionBuffer.Bytes())
	if err != nil {
		return Version{}, err
	}

	cf.cachedGitVersionByBinary[gitBinary] = cachedGitVersion{
		version: gitVersion,
		stat:    stat,
	}

	return gitVersion, nil
}

// newCommand creates a new command.Command for the given git command. If a repo is given, then the
// command will be run in the context of that repository. Note that this sets up arguments and
// environment variables for git, but doesn't run in the directory itself. If a directory
// is given, then the command will be run in that directory.
func (cf *ExecCommandFactory) newCommand(ctx context.Context, repo storage.Repository, sc Command, opts ...CmdOpt) (*command.Command, error) {
	// In Git change 2386535511, we introduced a feature, "attr: read attributes from HEAD when bare repo".
	// This causes a performance degradation. Current workaround is to get rid of the default behavior
	// in bare repos of reading from HEAD by setting attr.tree to empty tree.
	// See https://lore.kernel.org/git/xmqqzft6aozg.fsf_-_@gitster.g/ and
	// https://gitlab.com/gitlab-org/gitaly/-/issues/6064 for details.
	//
	// For gitaly, there are commands that specifically need attr.tree to be set to HEAD. We have a white list
	// of commands who should be excluded, see AttrTreeConfig. Those commands in the white list
	// need to set "attr.tree" to HEAD
	//
	// This can be removed once https://gitlab.com/gitlab-org/git/-/issues/316 is implemented and put in git upstream
	if featureflag.SetAttrTreeConfig.IsEnabled(ctx) {
		attrTreeConfig := cf.AttrTreeConfig(ctx, repo, sc, opts...)
		if attrTreeConfig != nil {
			opts = append(opts, WithConfig(*attrTreeConfig))
		}
	}

	config, err := cf.combineOpts(ctx, sc, opts)
	if err != nil {
		return nil, err
	}

	cmdGitVersion, err := cf.GitVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting Git version: %w", err)
	}

	// Starting with Git version 2.45.1, 2.44.1, 2.43.4, and 2.42.2 the `symlinkPointsToGitDir` fsck
	// check was added which warns if a repository contains symlinks pointing inside the Git
	// directory. While by default these are only supposed to be warnings, because Gitaly sets
	//`transfer.fsckObjects=true` the warnings are upgraded to errors and consequently can break
	// existing repositories. To avoid this, the fsck check is disabled. Since we support versions
	// of Git that do not contain this option, we apply the configuration to the corresponding
	// versions to avoid an unknown config warning.
	//
	// This Git configuration has been reverted in versions 2.45.2, 2.44.2, 2.43.5, and 2.42.3. Once
	// these versions are no longer supported by Gitaly, these options can be removed.
	if (cmdGitVersion.GreaterOrEqual(NewVersion(2, 45, 1, 0))) && cmdGitVersion.LessThan(NewVersion(2, 45, 2, 0)) ||
		(cmdGitVersion.GreaterOrEqual(NewVersion(2, 44, 1, 0)) && cmdGitVersion.LessThan(NewVersion(2, 44, 2, 0))) ||
		(cmdGitVersion.GreaterOrEqual(NewVersion(2, 43, 4, 0)) && cmdGitVersion.LessThan(NewVersion(2, 43, 5, 0))) ||
		(cmdGitVersion.GreaterOrEqual(NewVersion(2, 42, 2, 0)) && cmdGitVersion.LessThan(NewVersion(2, 42, 3, 0))) {
		config.globals = append(config.globals,
			ConfigPair{Key: "fsck.symlinkPointsToGitDir", Value: "ignore"},
			ConfigPair{Key: "fetch.fsck.symlinkPointsToGitDir", Value: "ignore"},
			ConfigPair{Key: "receive.fsck.symlinkPointsToGitDir", Value: "ignore"},
		)
	}

	args, err := cf.combineArgs(ctx, sc, config)
	if err != nil {
		return nil, err
	}

	env := config.env

	var repoPath string
	if repo != nil {
		var err error
		repoPath, err = cf.locator.GetRepoPath(repo)
		if err != nil {
			return nil, err
		}

		env = append(alternates.Env(repoPath, repo.GetGitObjectDirectory(), repo.GetGitAlternateObjectDirectories()), env...)
	}

	if config.worktreePath != "" {
		args = append([]string{"-C", config.worktreePath}, args...)
	} else if repoPath != "" {
		args = append([]string{"--git-dir", repoPath}, args...)
	}

	execEnv := cf.GetExecutionEnvironment(ctx)

	env = append(env, execEnv.EnvironmentVariables...)

	var cgroupsAddCommandOpts []cgroups.AddCommandOption
	if repo != nil {
		cgroupsAddCommandOpts = []cgroups.AddCommandOption{
			cgroups.WithCgroupKey(repo.GetStorageName() + "/" + repo.GetRelativePath()),
		}
	}

	commandOpts := config.commandOpts

	trace2Hooks := cf.trace2Hooks
	if trace2Hooks == nil {
		trace2Hooks = DefaultTrace2HooksFor(ctx, sc.Name, cf.logger, cf.traceRateLimiter)
	}
	if len(trace2Hooks) != 0 {
		trace2Manager, err := trace2.NewManager(correlation.ExtractFromContextOrGenerate(ctx), trace2Hooks)
		if err != nil {
			return nil, fmt.Errorf("creating trace2 manager: %w", err)
		}

		env = trace2Manager.Inject(env)
		commandOpts = append(commandOpts, command.WithFinalizer(cf.trace2Finalizer(trace2Manager)))
	}

	commandOpts = append(
		commandOpts,
		command.WithEnvironment(env),
		command.WithCommandName("git", sc.Name),
		command.WithCgroup(cf.cgroupsManager, cgroupsAddCommandOpts...),
		command.WithCommandGitVersion(cmdGitVersion.String()),
	)
	command, err := command.New(ctx, cf.logger, append([]string{execEnv.BinaryPath}, args...), commandOpts...)
	if err != nil {
		return nil, err
	}

	return command, nil
}

func (cf *ExecCommandFactory) combineOpts(ctx context.Context, sc Command, opts []CmdOpt) (cmdCfg, error) {
	var config cmdCfg

	commandDescription, ok := commandDescriptions[sc.Name]
	if !ok {
		return cmdCfg{}, fmt.Errorf("invalid sub command name %q: %w", sc.Name, ErrInvalidArg)
	}

	for _, opt := range opts {
		if err := opt(ctx, cf.cfg, cf, &config); err != nil {
			return cmdCfg{}, err
		}
	}

	if !config.hooksConfigured && commandDescription.mayUpdateRef() {
		return cmdCfg{}, fmt.Errorf("subcommand %q: %w", sc.Name, ErrHookPayloadRequired)
	}

	return config, nil
}

func (cf *ExecCommandFactory) combineArgs(ctx context.Context, sc Command, cc cmdCfg) (_ []string, err error) {
	var args []string

	defer func() {
		if err != nil && IsInvalidArgErr(err) && len(args) > 0 {
			cf.invalidCommandsMetric.WithLabelValues(sc.Name).Inc()
		}
	}()

	commandDescription, ok := commandDescriptions[sc.Name]
	if !ok {
		return nil, fmt.Errorf("invalid sub command name %q: %w", sc.Name, ErrInvalidArg)
	}

	globalConfig, err := cf.GlobalConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting global Git configuration: %w", err)
	}

	var commandOpts []GlobalOption
	if commandDescription.opts != nil {
		commandOpts = commandDescription.opts(ctx)
	}

	combinedGlobals := make([]GlobalOption, 0, len(globalConfig)+len(commandOpts)+len(cc.globals)+len(cf.cfg.Git.Config))
	for _, configPair := range globalConfig {
		combinedGlobals = append(combinedGlobals, configPair)
	}
	combinedGlobals = append(combinedGlobals, commandOpts...)
	combinedGlobals = append(combinedGlobals, cc.globals...)
	for _, configPair := range cf.cfg.Git.Config {
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

// GlobalConfiguration returns the global Git configuration that should be applied to every Git
// command.
func (cf *ExecCommandFactory) GlobalConfiguration(ctx context.Context) ([]ConfigPair, error) {
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
	config := []ConfigPair{
		// Disable automatic garbage collection as we handle scheduling
		// of it ourselves.
		{Key: "gc.auto", Value: "0"},

		// Disable automatic maintenance as we never enable any tasks.
		{Key: "maintenance.auto", Value: "0"},

		// CRLF line endings will get replaced with LF line endings when writing blobs to the
		// object database. No conversion is done when reading blobs from the object database.
		// This is required for the web editor. With feature flag "autocrlf_false" enabled
		// CRLF line endings will not get replaced and be left alone.
		{Key: "core.autocrlf", Value: "false"},

		// Git allows the use of replace refs, where a given object ID can be replaced with a
		// different one. The result is that Git commands would use the new object instead of the
		// old one in almost all contexts. This is a security threat: an adversary may use this
		// mechanism to replace malicious commits with seemingly benign ones. We thus globally
		// disable this mechanism.
		{Key: "core.useReplaceRefs", Value: "false"},

		// We configure for what data should be fsynced and how that should happen.
		// Synchronize object files, packed-refs and loose refs to disk to lessen the
		// likelihood of repository corruption in case the server crashes.
		{Key: "core.fsync", Value: "objects,derived-metadata,reference"},
		{Key: "core.fsyncMethod", Value: "fsync"},

		// When deleting references, Git needs to rewrite the `packed-refs` file to evict
		// the reference from it. In order to not race with concurrent writers it thus needs
		// to lock the file for concurrent access. This lock is thus a shared resource, and
		// in high-activity repositories we see a lot of contention around this lock: for
		// once because we typically have many writes there, but second because these repos
		// tend to have many references and thus rewriting the `packed-refs` file takes
		// proportionally longer.
		//
		// Git has a default timeout of 1 second to try and lock the file. In practice
		// though we see that this is not sufficient, and epsecially the `DeleteRefs` RPC is
		// erroring out very frequently. We thus increase the timeout to 10 seconds. While
		// comparatively high, context cancellation would still cause us to exit early in
		// case the caller doesn't want to wait this long.
		{Key: "core.packedRefsTimeout", Value: "10000"},
		// Similarly, for loose references we bump the limit from 100 milliseconds to 1 second. We aim for a
		// lower limit here as the locking for loose references is typically a lot more fine-grained. We have
		// still observed lock contention around them though, but mostly in cases where the host system was
		// heavily loaded by a storm of incoming RPCs.
		{Key: "core.filesRefLockTimeout", Value: "1000"},

		// Change the size of files we consider to be big from 512MB to 50MB. This setting influences a bunch of
		// things for blobs that are larger than this size:
		//
		// - They will not be slurped into memory anymore, but will instead use streaming interfaces. This
		//   should reduce memory consumption as we don't have to allocate up to 512MB buffers anymore.
		//
		// - They will not be diffed anymore. This should significantly reduce the time it
		//   takes to computes diffs when such diffs contain huge blobs. This is of course at the cost of not
		//   being able to show any such diffs anymore, but overall it seems unreasonable to compute diffs for
		//   any such huge files anyway.
		//
		// - They will not be deltified anymore. This should ultimately be a no-op for us as we have already
		//   been setting `pack.windowSize=100m` already, which restricts the maximum window size. The value of
		//   50MB has thus been chosen such that it matches 2 times the window size.
		//
		// So ultimately, this should not lead to larger packfiles as we have already been restricting the
		// packfile window anyway while it should on the other hand lead to lower memory consumption and faster
		// computation of diffs when large blobs are involved.
		{Key: "core.bigFileThreshold", Value: fmt.Sprintf("%dm", BigFileThresholdMB)},
	}

	return config, nil
}

func (cf *ExecCommandFactory) trace2Finalizer(manager *trace2.Manager) func(context.Context, *command.Command) {
	return func(ctx context.Context, cmd *command.Command) {
		manager.Finish(ctx)
		customFields := log.CustomFieldsFromContext(ctx)
		if customFields != nil {
			customFields.RecordMetadata("trace2.activated", "true")
			customFields.RecordMetadata("trace2.hooks", strings.Join(manager.HookNames(), ","))
			if manager.Error() != nil {
				customFields.RecordMetadata("trace2.error", manager.Error().Error())
			}
		}
	}
}

// AttrTreeConfig adds `attr.tree = HEAD` to the commands in the whiteList have,
// others has `attr.tree = emptyTreeHash`.
func (cf *ExecCommandFactory) AttrTreeConfig(ctx context.Context, repo storage.Repository, sc Command, opts ...CmdOpt) *ConfigPair {
	repoPath := cf.findRepoPath(ctx, repo, sc, opts...)
	if len(repoPath) == 0 {
		// Add tree config only when repo exists, because some git command, e.g. diff
		// can exec without a repo, If we add an attr.tree when a command is executing
		// without a repo, it leads to error, e.g. attempting to get main_ref_store outside of repository.
		return nil
	}

	whiteList := map[string]struct{}{
		"diff":         {},
		"merge":        {},
		"check-attr":   {},
		"worktree":     {},
		"archive":      {},
		"log":          {},
		"format-patch": {},
	}
	if _, ok := whiteList[sc.Name]; ok {
		return &ConfigPair{Key: "attr.tree", Value: "HEAD"}
	}

	// Execute git -C <repo path> rev-parse --show-object-format before setting
	// attr.tree to an empty tree. We need to decide the object format of the repo. SHA1 and SHA256
	// have different empty tree object ID.
	//
	// Note that we're not using `newCommand()` or `DetectObjectHash()`, but instead hand-craft the command.
	// This is required to avoid recursive git command spawning.
	execEnv := cf.GetExecutionEnvironment(ctx)
	var objectFormat bytes.Buffer
	findFormatCmd, err := command.New(ctx, cf.logger, []string{
		execEnv.BinaryPath,
		"-C", repoPath,
		"rev-parse", "--show-object-format",
	},
		command.WithEnvironment(execEnv.EnvironmentVariables),
		command.WithStdout(&objectFormat),
	)
	if err != nil {
		return nil
	}
	if err := findFormatCmd.Wait(); err != nil {
		return nil
	}

	switch strings.TrimSpace(objectFormat.String()) {
	case ObjectHashSHA1.Format:
		return &ConfigPair{Key: "attr.tree", Value: ObjectHashSHA1.EmptyTreeOID.String()}
	case ObjectHashSHA256.Format:
		return &ConfigPair{Key: "attr.tree", Value: ObjectHashSHA256.EmptyTreeOID.String()}
	default:
		return nil
	}
}

// findRepoPath find the repo to execute git rev-parse --show-object-format
func (cf *ExecCommandFactory) findRepoPath(ctx context.Context, repo storage.Repository, sc Command, opts ...CmdOpt) string {
	config, _ := cf.combineOpts(ctx, sc, opts)

	var repoPath string
	if repo != nil {
		repoPath, _ = cf.locator.GetRepoPath(repo)
	}

	if config.worktreePath != "" {
		return config.worktreePath
	} else if repoPath != "" {
		return repoPath
	}
	return ""
}
