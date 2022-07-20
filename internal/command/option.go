package command

import (
	"io"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
)

type config struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	dir         string
	environment []string

	finalizer func(*Command)

	commandName    string
	subcommandName string
	gitVersion     string

	cgroupsManager CgroupsManager
	cgroupsRepo    repository.GitRepo
}

// Option is an option that can be passed to `New()` for controlling how the command is being
// created.
type Option func(cfg *config)

// WithStdin sets up the command to read from the given reader.
func WithStdin(stdin io.Reader) Option {
	return func(cfg *config) {
		cfg.stdin = stdin
	}
}

// WithSetupStdin instructs New() to configure the stdin pipe of the command it is creating. This
// allows you call Write() on the command as if it is an ordinary io.Writer, sending data directly
// to the stdin of the process.
func WithSetupStdin() Option {
	return func(cfg *config) {
		cfg.stdin = stdinSentinel{}
	}
}

// WithStdout sets up the command to write standard output to the given writer.
func WithStdout(stdout io.Writer) Option {
	return func(cfg *config) {
		cfg.stdout = stdout
	}
}

// WithStderr sets up the command to write standard error to the given writer.
func WithStderr(stderr io.Writer) Option {
	return func(cfg *config) {
		cfg.stderr = stderr
	}
}

// WithDir will set up the command to be ran in the specific directory.
func WithDir(dir string) Option {
	return func(cfg *config) {
		cfg.dir = dir
	}
}

// WithEnvironment sets up environment variables that shall be set for the command.
func WithEnvironment(environment []string) Option {
	return func(cfg *config) {
		cfg.environment = environment
	}
}

// WithCommandName overrides the "cmd" and "subcmd" label used in metrics.
func WithCommandName(commandName, subcommandName string) Option {
	return func(cfg *config) {
		cfg.commandName = commandName
		cfg.subcommandName = subcommandName
	}
}

// WithCommandGitVersion overrides the "git_version" label used in metrics.
func WithCommandGitVersion(gitCmdVersion string) Option {
	return func(cfg *config) {
		cfg.gitVersion = gitCmdVersion
	}
}

// CgroupsManager is a subset of the `cgroups.Manager` interface. We need to replicate it here to
// avoid a cyclic dependency between both packages.
type CgroupsManager interface {
	AddCommand(*Command, repository.GitRepo) (string, error)
}

// WithCgroup adds the spawned command to a Cgroup. The bucket used will be derived from the
// command's arguments and/or from the repository.
func WithCgroup(cgroupsManager CgroupsManager, repo repository.GitRepo) Option {
	return func(cfg *config) {
		cfg.cgroupsManager = cgroupsManager
		cfg.cgroupsRepo = repo
	}
}

// WithFinalizer sets up the finalizer to be run when the command is being wrapped up. It will be
// called after `Wait()` has returned.
func WithFinalizer(finalizer func(*Command)) Option {
	return func(cfg *config) {
		cfg.finalizer = finalizer
	}
}
