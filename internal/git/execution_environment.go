package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"golang.org/x/sys/unix"
)

var (
	// ErrNotConfigured may be returned by an ExecutionEnvironmentConstructor in case an
	// environment was not configured.
	ErrNotConfigured = errors.New("execution environment is not configured")

	// BundledGitConstructors defines the versions of Git that we embed into the Gitaly
	// binary.
	BundledGitConstructors = []BundledGitEnvironmentConstructor{
		{
			Suffix: "-v2.45",
		},

		{
			Suffix: "-v2.44",
		},
	}

	// defaultExecutionEnvironmentConstructors is the list of Git environments supported by the
	// Git command factory. The order is important and signifies the priority in which the
	// environments will be used: the environment created by the first constructor is the one
	// that will be preferred when executing Git commands. Later environments may be used in
	// case `IsEnabled()` returns `false` though.
	defaultExecutionEnvironmentConstructors = func() (constructors []ExecutionEnvironmentConstructor) {
		for _, c := range BundledGitConstructors {
			constructors = append(constructors, c)
		}

		constructors = append(constructors, DistributedGitEnvironmentConstructor{})

		return constructors
	}()
)

// ExecutionEnvironmentConstructor is an interface for constructors of Git execution environments.
// A constructor should be able to set up an environment in which it is possible to run Git
// executables.
type ExecutionEnvironmentConstructor interface {
	Construct(config.Cfg) (ExecutionEnvironment, error)
}

// ExecutionEnvironment describes the environment required to execute a Git command
type ExecutionEnvironment struct {
	// BinaryPath is the path to the Git binary.
	BinaryPath string
	// EnvironmentVariables are variables which must be set when running the Git binary.
	EnvironmentVariables []string

	isEnabled func(context.Context) bool
	cleanup   func() error
}

// Cleanup cleans up any state set up by this ExecutionEnvironment.
func (e ExecutionEnvironment) Cleanup() error {
	if e.cleanup != nil {
		return e.cleanup()
	}
	return nil
}

// IsEnabled checks whether the ExecutionEnvironment is enabled in the given context. An execution
// environment will typically be enabled by default, except if it's feature-flagged.
func (e ExecutionEnvironment) IsEnabled(ctx context.Context) bool {
	if e.isEnabled != nil {
		return e.isEnabled(ctx)
	}

	return true
}

// DistributedGitEnvironmentConstructor creates ExecutionEnvironments via the Git binary path
// configured in the Gitaly configuration. This expects a complete Git installation with all its
// components. The installed distribution must either have its prefix compiled into the binaries or
// alternatively be compiled with runtime-detection of the prefix such that Git is able to locate
// its auxiliary helper binaries correctly.
type DistributedGitEnvironmentConstructor struct{}

// Construct sets up an ExecutionEnvironment for a complete Git distribution. No setup needs to be
// performed given that the Git environment is expected to be self-contained.
//
// For testing purposes, this function overrides the configured Git binary path if the
// `GITALY_TESTING_GIT_BINARY` environment variable is set.
func (c DistributedGitEnvironmentConstructor) Construct(cfg config.Cfg) (ExecutionEnvironment, error) {
	binaryPath := cfg.Git.BinPath
	var environmentVariables []string
	if override := os.Getenv("GITALY_TESTING_GIT_BINARY"); binaryPath == "" && override != "" {
		binaryPath = override
		environmentVariables = []string{
			// When using Git's bin-wrappers as testing binary then the wrapper will
			// automatically set up the location of the Git templates and export the
			// environment variable. This would override our own defaults though and
			// thus leads to diverging behaviour. To fix this we simply ask the bin
			// wrappers not to do this.
			"NO_SET_GIT_TEMPLATE_DIR=YesPlease",
		}
	}

	if binaryPath == "" {
		return ExecutionEnvironment{}, ErrNotConfigured
	}

	return ExecutionEnvironment{
		BinaryPath:           binaryPath,
		EnvironmentVariables: environmentVariables,
	}, nil
}

// BundledGitEnvironmentConstructor sets up an ExecutionEnvironment for a bundled Git installation.
// Bundled Git is a partial Git installation, where only a subset of Git binaries are installed
// into Gitaly's binary directory. The binaries must have a `gitaly-` prefix like e.g. `gitaly-git`.
// Bundled Git installations can be installed with Gitaly's Makefile via `make install
// WITH_BUNDLED_GIT=YesPlease`.
type BundledGitEnvironmentConstructor struct {
	// Suffix is the version suffix used for this specific bundled Git environment. In case
	// multiple sets of bundled Git versions are installed it is possible to also have multiple
	// of these bundled Git environments with different suffixes.
	Suffix string
	// FeatureFlags is the set of feature flags which must be enabled in order for the bundled
	// Git environment to be enabled. Note that _all_ feature flags must be set to `true` in the
	// context.
	FeatureFlags []featureflag.FeatureFlag
}

// Construct sets up an ExecutionEnvironment for a bundled Git installation. Because bundled Git
// installations are not complete Git installations we need to set up a usable environment at
// runtime. This is done by creating a temporary directory into which we symlink the bundled
// binaries with their usual names as expected by Git. Furthermore, we configure the GIT_EXEC_PATH
// environment variable to point to that directory such that Git is able to locate its auxiliary
// binaries.
func (c BundledGitEnvironmentConstructor) Construct(cfg config.Cfg) (_ ExecutionEnvironment, returnedErr error) {
	if !cfg.Git.UseBundledBinaries {
		return ExecutionEnvironment{}, ErrNotConfigured
	}

	// We need to source the Git binaries from different locations depending on the environment we're running in:
	// - In production, the Git binaries get unpacked by packed_binaries.go into Gitaly's RuntimeDir, which for
	//   Omnibus is something like /var/opt/gitlab/gitaly/run/gitaly-<xxx>.
	// - In testing, the binaries remain in the _build/bin directory of the repository root.
	bundledGitPath := cfg.RuntimeDir
	if testing.Testing() {
		var err error

		// Infer the location of the bundled Git binaries. The Makefile ensures this is fixed at
		// _build/bin by default.
		_, curr, _, ok := runtime.Caller(0)
		if !ok {
			return ExecutionEnvironment{}, errors.New("failed to get current directory")
		}

		bundledGitPath, err = filepath.Abs(filepath.Join(filepath.Dir(curr), "..", "..", "_build", "bin"))
		if err != nil {
			return ExecutionEnvironment{}, fmt.Errorf("get bundled bin path: %w", err)
		}
	}

	// In order to support having a single Git binary only as compared to a complete Git
	// installation, we create our own GIT_EXEC_PATH which contains symlinks to the Git
	// binary for executables which Git expects to be present.
	gitExecPath, err := os.MkdirTemp(cfg.RuntimeDir, "git-exec-*.d")
	if err != nil {
		return ExecutionEnvironment{}, fmt.Errorf("creating Git exec path: %w", err)
	}

	cleanup := func() error {
		if err := os.RemoveAll(gitExecPath); err != nil {
			return fmt.Errorf("removal of execution path: %w", err)
		}

		return nil
	}
	defer func() {
		if returnedErr != nil {
			_ = cleanup()
		}
	}()

	for executable, target := range map[string]string{
		"git":                "gitaly-git",
		"git-receive-pack":   "gitaly-git",
		"git-upload-pack":    "gitaly-git",
		"git-upload-archive": "gitaly-git",
		"git-http-backend":   "gitaly-git-http-backend",
		"git-remote-http":    "gitaly-git-remote-http",
		"git-remote-https":   "gitaly-git-remote-http",
		"git-remote-ftp":     "gitaly-git-remote-http",
		"git-remote-ftps":    "gitaly-git-remote-http",
	} {
		target := target + c.Suffix
		targetPath := filepath.Join(bundledGitPath, target)

		if err := unix.Access(targetPath, unix.X_OK); err != nil {
			return ExecutionEnvironment{}, fmt.Errorf("checking bundled Git binary %q: %w", target, err)
		}

		if err := os.Symlink(
			targetPath,
			filepath.Join(gitExecPath, executable),
		); err != nil {
			return ExecutionEnvironment{}, fmt.Errorf("linking Git executable %q: %w", executable, err)
		}
	}

	return ExecutionEnvironment{
		BinaryPath: filepath.Join(gitExecPath, "git"),
		EnvironmentVariables: []string{
			"GIT_EXEC_PATH=" + gitExecPath,
		},
		cleanup: cleanup,
		isEnabled: func(ctx context.Context) bool {
			for _, flag := range c.FeatureFlags {
				if flag.IsDisabled(ctx) {
					return false
				}
			}

			return true
		},
	}, nil
}

// FallbackGitEnvironmentConstructor sets up a fallback execution environment where Git is resolved
// via the `PATH` environment variable. This is only intended as a last resort in case no other
// environments have been set up.
type FallbackGitEnvironmentConstructor struct{}

// Construct sets up an execution environment by searching `PATH` for a `git` executable.
func (c FallbackGitEnvironmentConstructor) Construct(config.Cfg) (ExecutionEnvironment, error) {
	resolvedPath, err := exec.LookPath("git")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ExecutionEnvironment{}, fmt.Errorf("%w: no git executable found in PATH", ErrNotConfigured)
		}

		return ExecutionEnvironment{}, fmt.Errorf("resolving git executable: %w", err)
	}

	return ExecutionEnvironment{
		BinaryPath: resolvedPath,
		// We always pretend that this environment is disabled. This has the effect that
		// even if all the other existing execution environments are disabled via feature
		// flags, we will still not use the fallback Git environment but will instead use
		// the feature flagged environments. The fallback will then only be used in the
		// case it is the only existing environment with no other better alternatives.
		isEnabled: func(context.Context) bool {
			return false
		},
	}, nil
}
