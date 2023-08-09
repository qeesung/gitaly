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
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2hooks"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/labkit/correlation"
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
func DefaultTrace2HooksFor(ctx context.Context, subCmd string) []trace2.Hook {
	var hooks []trace2.Hook
	if tracing.IsSampled(ctx) {
		hooks = append(hooks, trace2hooks.NewTracingExporter())
	}
	if subCmd == "pack-objects" {
		hooks = append(hooks, trace2hooks.NewPackObjectsMetrics())
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
	cgroupsManager        cgroups.Manager
	trace2Hooks           []trace2.Hook
	invalidCommandsMetric *prometheus.CounterVec
	hookDirs              hookDirectories

	cachedGitVersionLock     sync.RWMutex
	cachedGitVersionByBinary map[string]cachedGitVersion
}

// NewExecCommandFactory returns a new instance of initialized ExecCommandFactory. The returned
// cleanup function shall be executed when the server shuts down.
func NewExecCommandFactory(cfg config.Cfg, opts ...ExecCommandFactoryOption) (_ *ExecCommandFactory, _ func(), returnedErr error) {
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

	hookDirectories, cleanup, err := setupHookDirectories(cfg, factoryCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up hooks: %w", err)
	}
	cleanups = append(cleanups, cleanup)

	execEnvs, cleanup, err := setupGitExecutionEnvironments(cfg, factoryCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("setting up Git execution environment: %w", err)
	}
	cleanups = append(cleanups, cleanup)

	cgroupsManager := factoryCfg.cgroupsManager
	if cgroupsManager == nil {
		cgroupsManager = cgroups.NewManager(cfg.Cgroups, os.Getpid())
	}

	gitCmdFactory := &ExecCommandFactory{
		cfg:            cfg,
		execEnvs:       execEnvs,
		locator:        config.NewLocator(cfg),
		cgroupsManager: cgroupsManager,
		trace2Hooks:    factoryCfg.trace2Hooks,
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
func setupGitExecutionEnvironments(cfg config.Cfg, factoryCfg execCommandFactoryConfig) ([]ExecutionEnvironment, func(), error) {
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

		logrus.WithFields(logrus.Fields{
			"resolvedPath": execEnv.BinaryPath,
		}).Warn("Git has not been properly configured, falling back to Git found on PATH")

		execEnvs = append(execEnvs, execEnv)
	}

	return execEnvs, func() {
		for _, execEnv := range execEnvs {
			execEnv.Cleanup()
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

func setupHookDirectories(cfg config.Cfg, factoryCfg execCommandFactoryConfig) (hookDirectories, func(), error) {
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
	for _, hook := range []string{"pre-receive", "post-receive", "update", "reference-transaction"} {
		if err := os.Symlink(cfg.BinaryPath("gitaly-hooks"), filepath.Join(tempHooksPath, hook)); err != nil {
			return hookDirectories{}, nil, fmt.Errorf("creating symlink for %s hook: %w", hook, err)
		}
	}

	return hookDirectories{
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
	cmd, err := command.New(ctx, []string{execEnv.BinaryPath, "version"},
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
	config, err := cf.combineOpts(ctx, sc, opts)
	if err != nil {
		return nil, err
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

	cmdGitVersion, err := cf.GitVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting Git version: %w", err)
	}

	var cgroupsAddCommandOpts []cgroups.AddCommandOption
	if repo != nil {
		cgroupsAddCommandOpts = []cgroups.AddCommandOption{
			cgroups.WithCgroupKey(repo.GetStorageName() + "/" + repo.GetRelativePath()),
		}
	}

	commandOpts := config.commandOpts

	trace2Hooks := cf.trace2Hooks
	if trace2Hooks == nil {
		trace2Hooks = DefaultTrace2HooksFor(ctx, sc.Name)
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
	command, err := command.New(ctx, append([]string{execEnv.BinaryPath}, args...), commandOpts...)
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

	globalConfig, err := GlobalConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting global Git configuration: %w", err)
	}

	combinedGlobals := make([]GlobalOption, 0, len(globalConfig)+len(commandDescription.opts)+len(cc.globals)+len(cf.cfg.Git.Config))
	for _, configPair := range globalConfig {
		combinedGlobals = append(combinedGlobals, configPair)
	}
	combinedGlobals = append(combinedGlobals, commandDescription.opts...)
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

func GlobalOptions(ctx context.Context) ([]GlobalOption, error) {
	configs, err := GlobalConfiguration(ctx)
	if err != nil {
		return nil, err
	}

	var options []GlobalOption

	for _, config := range configs {
		options = append(options, config)
	}

	options = append(options, Flag{Name: "--attr-source=HEAD"})

	return options, nil
}

// GlobalConfiguration returns the global Git configuration that should be applied to every Git
// command.
func GlobalConfiguration(ctx context.Context) ([]ConfigPair, error) {
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

		// CRLF line endings will get replaced with LF line endings
		// when writing blobs to the object database. No conversion is
		// done when reading blobs from the object database. This is
		// required for the web editor.
		{Key: "core.autocrlf", Value: "input"},

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
