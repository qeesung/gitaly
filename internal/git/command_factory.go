package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
)

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

	// SidecarGitConfiguration returns the Git configuration is it should be used by the Ruby
	// sidecar. This is a design wart and shouldn't ever be used outside of the context of the
	// sidecar.
	SidecarGitConfiguration(context.Context) ([]ConfigPair, error)
}

type execCommandFactoryConfig struct {
	hooksPath     string
	gitBinaryPath string
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

	gitCmdFactory := &ExecCommandFactory{
		cfg:            cfg,
		execEnvs:       execEnvs,
		locator:        config.NewLocator(cfg),
		cgroupsManager: cgroups.NewManager(cfg.Cgroups),
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
	}

	// Prevent the environment from affecting git calls by ignoring the configuration files.
	// This should be done always but we have to wait until 15.0 due to backwards compatibility
	// concerns.
	//
	// See https://gitlab.com/gitlab-org/gitaly/-/issues/3617.
	if cfg.Git.IgnoreGitconfig {
		sharedEnvironment = append(sharedEnvironment,
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"XDG_CONFIG_HOME=/dev/null",
		)
	}

	if factoryCfg.gitBinaryPath != "" {
		return []ExecutionEnvironment{
			{BinaryPath: factoryCfg.gitBinaryPath, EnvironmentVariables: sharedEnvironment},
		}, func() {}, nil
	}

	var execEnvs []ExecutionEnvironment
	for _, constructor := range ExecutionEnvironmentConstructors {
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
		return nil, nil, fmt.Errorf("could not set up any Git execution environments")
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
func (cf *ExecCommandFactory) GetExecutionEnvironment(ctx context.Context) ExecutionEnvironment {
	// We first go through all execution environments and check whether any of them is enabled
	// in the current context, which most importantly will check their respective feature flags.
	for _, execEnv := range cf.execEnvs {
		if execEnv.IsEnabled(ctx) {
			return execEnv
		}
	}

	// If none is enabled though, we simply use the first execution environment, which is also
	// the one with highest priority. This can for example happen in case we only were able to
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
	cmd, err := command.New(ctx, []string{execEnv.BinaryPath, "version"}, command.WithEnvironment(execEnv.EnvironmentVariables))
	if err != nil {
		return Version{}, fmt.Errorf("spawning version command: %w", err)
	}

	gitVersion, err := parseVersionFromCommand(cmd)
	if err != nil {
		return Version{}, err
	}

	if err := cmd.Wait(); err != nil {
		return Version{}, fmt.Errorf("waiting for version: %w", err)
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

	env = append(env, execEnv.EnvironmentVariables...)

	cmdGitVersion, err := cf.GitVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting Git version: %w", err)
	}

	command, err := command.New(ctx, append([]string{execEnv.BinaryPath}, args...), append(
		config.commandOpts,
		command.WithDir(dir),
		command.WithEnvironment(env),
		command.WithCommandName("git", sc.Subcommand()),
		command.WithCgroup(cf.cgroupsManager, repo),
		command.WithCommandGitVersion(cmdGitVersion.String()),
	)...)
	if err != nil {
		return nil, err
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

	combinedGlobals, err := cf.globalConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting global Git configuration: %w", err)
	}

	combinedGlobals = append(combinedGlobals, commandDescription.opts...)
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

// globalConfiguration returns the global Git configuration that should be applied to every Git
// command.
func (cf *ExecCommandFactory) globalConfiguration(ctx context.Context) ([]GlobalOption, error) {
	// It's fine to ask for the Git version whenever we spawn a command: the value is cached
	// nowadays, so this would typically only boil down to a single stat(3P) call to determine
	// whether the cache is stale or not.
	gitVersion, err := cf.GitVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("determining Git version: %w", err)
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
	config := []GlobalOption{
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

		// Commit-graphs are used as an optimization to speed up reading commits and to be
		// able to perform certain commit-related queries faster. One property that these
		// graphs are storing is the generation number of a commit, where there are two
		// different types of generation numbers:
		//
		//     - Topological level: a commit with no parents has a level of 1. A commit with
		//       at least one parent has a level one more than the largest topological level
		//       of its parents.
		//
		//     - Corrected committer date: a commit with no parents has a corrected commit
		//       date equal to its committer date. A commit with at least one parent has a
		//       corrected committer date equal to the maximum between either its own
		//       committer date or the largest corrected committer date across its parents
		//       plus 1.
		//
		// By default, newer Git versions store both generation numbers for commits, where
		// the corrected committer date allows for some more optimizations. But due to a bug
		// in Git v2.35.0 and earlier, the corrected committer date wasn't ever read.
		//
		// This bug was fixed in Git v2.36.0, together with a few other bugs in this area.
		// But unfortunately, a new bug was introduced: when upgrading a commit-graph
		// written by Git v2.35.0 or newer with Git v2.36.0 and later with `--changed-paths`
		// enabled then the resulting commit-graph may be corrupt.
		//
		// Let's disable reading and writing corrected committer dates for now until the fix
		// to this issue is upstream.
		ConfigPair{Key: "commitGraph.generationVersion", Value: "1"},
	}

	// Git v2.36.0 introduced new fine-grained configuration for what data should be fsynced and
	// how that should happen.
	if gitVersion.HasGranularFsyncConfig() {
		config = append(
			config,
			// This is the same as below, but in addition we're also syncing packed-refs
			// and loose refs to disk. This fixes a long-standing issue we've had where
			// hard reboots of a server could end up corrupting loose references.
			ConfigPair{Key: "core.fsync", Value: "objects,derived-metadata,reference"},
			ConfigPair{Key: "core.fsyncMethod", Value: "fsync"},
		)
	} else {
		// Synchronize object files to lessen the likelihood of
		// repository corruption in case the server crashes.
		config = append(
			config, ConfigPair{Key: "core.fsyncObjectFiles", Value: "true"},
		)
	}

	return config, nil
}

// SidecarGitConfiguration assembles the Git configuration as required by the Ruby sidecar. This
// includes global configuration, command-specific configuration for all commands executed in the
// sidecar, as well as configuration that was configured by the administrator in Gitaly's config.
//
// This function should not be used for anything else but the Ruby sidecar.
func (cf *ExecCommandFactory) SidecarGitConfiguration(ctx context.Context) ([]ConfigPair, error) {
	// Collect the global configuration that is specific to the current Git version...
	options, err := cf.globalConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting global config: %w", err)
	}

	// ... as well as all configuration that exists for specific Git subcommands. The sidecar
	// only executes git-update-ref(1) nowadays, and this set of commands is not expected to
	// grow anymore. So while really intimate with how the sidecar does this, it is good enough
	// until we finally remove it.
	options = append(options, commandDescriptions["update-ref"].opts...)

	// Convert the `GlobalOption`s into `ConfigPair`s.
	configPairs := make([]ConfigPair, 0, len(options)+len(cf.cfg.Git.Config))
	for _, option := range options {
		configPair, ok := option.(ConfigPair)
		if !ok {
			continue
		}

		configPairs = append(configPairs, configPair)
	}

	// Lastly, we also apply the Git configuration as set by the administrator in Gitaly's
	// config. Note that we do not check for conflicts here: administrators should be able to
	// override whatever is configured by Gitaly.
	for _, configEntry := range cf.cfg.Git.Config {
		configPairs = append(configPairs, ConfigPair{
			Key:   configEntry.Key,
			Value: configEntry.Value,
		})
	}

	return configPairs, nil
}
