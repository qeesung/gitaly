package testhelper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	git "github.com/libgit2/git2go/v33"
	log "github.com/sirupsen/logrus"
	gitalylog "gitlab.com/gitlab-org/gitaly/v15/internal/log"
)

var testDirectory string

// RunOption is an option that can be passed to Run.
type RunOption func(*runConfig)

type runConfig struct {
	setup                  func() error
	disableGoroutineChecks bool
}

// WithSetup allows the caller of Run to pass a setup function that will be called after global
// test state has been configured.
func WithSetup(setup func() error) RunOption {
	return func(cfg *runConfig) {
		cfg.setup = setup
	}
}

// WithDisabledGoroutineChecker disables checking for leaked Goroutines after tests have run. This
// should ideally only be used as a temporary measure until all Goroutine leaks have been fixed.
//
// Deprecated: This should not be used, but instead you should try to fix all Goroutine leakages.
func WithDisabledGoroutineChecker() RunOption {
	return func(cfg *runConfig) {
		cfg.disableGoroutineChecks = true
	}
}

// Run sets up required testing state and executes the given test suite. It can optionally receive a
// variable number of RunOptions.
func Run(m *testing.M, opts ...RunOption) {
	// Run tests in a separate function such that we can use deferred statements and still
	// (indirectly) call `os.Exit()` in case the test setup failed.
	if err := func() error {
		var cfg runConfig
		for _, opt := range opts {
			opt(&cfg)
		}

		defer mustHaveNoChildProcess()
		if !cfg.disableGoroutineChecks {
			defer mustHaveNoGoroutines()
		}

		cleanup, err := configure()
		if err != nil {
			return fmt.Errorf("test configuration: %w", err)
		}
		defer cleanup()

		if cfg.setup != nil {
			if err := cfg.setup(); err != nil {
				return fmt.Errorf("error calling setup function: %w", err)
			}
		}

		m.Run()

		return nil
	}(); err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}
}

// configure sets up the global test configuration. On failure,
// terminates the program.
func configure() (_ func(), returnedErr error) {
	gitalylog.Configure(gitalylog.Loggers, "json", "panic")

	for key, value := range map[string]string{
		// We inject the following two variables, which instruct Git to search its
		// configuration in non-default locations in the global and system scope. This is
		// done as a sanity check to verify that we don't ever pick up this configuration
		// but instead filter it out whenever we execute Git. The values are set to the
		// root directory: this directory is guaranteed to exist, and Git is guaranteed to
		// fail parsing those directories as configuration.
		"GIT_CONFIG_GLOBAL": "/",
		"GIT_CONFIG_SYSTEM": "/",
		// Same as above, this value is injected such that we can detect whether any site
		// which executes Git fails to inject the expected Git directory. This is also
		// required such that we don't ever inadvertently execute Git commands in the wrong
		// Git repository.
		"GIT_DIR": "/dev/null",
	} {
		if err := os.Setenv(key, value); err != nil {
			return nil, err
		}
	}

	// We need to make sure that we're gitconfig-clean: neither Git nor libgit2 should pick up
	// gitconfig files from anywhere but the repository itself in case they're configured to
	// ignore them. We set that configuration by default in our tests to have a known-good
	// environment.
	//
	// In order to verify that this really works as expected we optionally intercept the user's
	// HOME with a custom home directory that contains gitconfig files that cannot be parsed. So
	// if these were picked up, we'd see errors and thus know that something's going on. We
	// need to make this opt-in though given that some users require HOME to be set, e.g. for
	// Bundler. Our CI does run with this setting enabled though.
	if len(os.Getenv("GITALY_TESTING_INTERCEPT_HOME")) > 0 {
		_, currentFile, _, ok := runtime.Caller(0)
		if !ok {
			return nil, fmt.Errorf("cannot compute intercepted home location")
		}

		homeDir := filepath.Join(filepath.Dir(currentFile), "testdata", "home")
		if _, err := os.Stat(homeDir); err != nil {
			return nil, fmt.Errorf("statting intercepted home location: %w", err)
		}

		if err := os.Setenv("HOME", homeDir); err != nil {
			return nil, fmt.Errorf("setting home: %w", err)
		}

		if err := git.SetSearchPath(git.ConfigLevelGlobal, homeDir); err != nil {
			return nil, fmt.Errorf("cannot set libgit2 search path: %w", err)
		}
	}

	cleanup, err := configureTestDirectory()
	if err != nil {
		return nil, fmt.Errorf("configuring test directory: %w", err)
	}
	defer func() {
		if returnedErr != nil {
			cleanup()
		}
	}()

	return cleanup, nil
}

func configureTestDirectory() (_ func(), returnedErr error) {
	if testDirectory != "" {
		return nil, errors.New("test directory has already been configured")
	}

	// Ideally, we'd just pass "" to `os.MkdirTemp()`, which would then use either the value of
	// `$TMPDIR` or alternatively "/tmp". But given that macOS sets `$TMPDIR` to a user specific
	// temporary directory, resulting paths would be too long and thus cause issues galore. We
	// thus support our own specific variable instead which allows users to override it, with
	// our default being "/tmp".
	tempDirLocation := os.Getenv("TEST_TMP_DIR")
	if tempDirLocation == "" {
		tempDirLocation = "/tmp"
	}

	var err error
	testDirectory, err = os.MkdirTemp(tempDirLocation, "gitaly-")
	if err != nil {
		return nil, fmt.Errorf("creating test directory: %w", err)
	}

	cleanup := func() {
		if err := os.RemoveAll(testDirectory); err != nil {
			log.Errorf("cleaning up test directory: %v", err)
		}
	}
	defer func() {
		if returnedErr != nil {
			cleanup()
		}
	}()

	// macOS symlinks /tmp/ to /private/tmp/ which can cause some check to fail. We thus resolve
	// the symlinks to their actual location.
	testDirectory, err = filepath.EvalSymlinks(testDirectory)
	if err != nil {
		return nil, err
	}

	// In many locations throughout Gitaly, we create temporary files and directories. By
	// default, these would clutter the "real" temporary directory with useless cruft that stays
	// around after our tests. To avoid this, we thus set the TMPDIR environment variable to
	// point into a directory inside of out test directory.
	globalTempDir := filepath.Join(testDirectory, "tmp")
	if err := os.Mkdir(globalTempDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating global temporary directory: %w", err)
	}
	if err := os.Setenv("TMPDIR", globalTempDir); err != nil {
		return nil, fmt.Errorf("setting global temporary directory: %w", err)
	}

	return cleanup, nil
}
