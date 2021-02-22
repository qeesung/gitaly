package testhelper

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
)

// SetupGitOption is a configuration option fot the git command execution setup.
type SetupGitOption func(*GitSetup)

// GitSetupPerson contains value for configuring git committer or an author.
type GitSetupPerson struct {
	Name  string
	Email string
}

// GitSetup assembles set of configuration options that might be used for the git command execution.
type GitSetup struct {
	Cfg       config.Cfg
	Stdin     io.Reader
	Committer *GitSetupPerson
	Author    *GitSetupPerson
	repoPath  string
}

// WithStdin sets a reader that will be used as a standard input for the git command.
func WithStdin(reader io.Reader) SetupGitOption {
	return func(gs *GitSetup) {
		gs.Stdin = reader
	}
}

// WithCommitter setups a committer name and email to be used for the git operation.
func WithCommitter(name, email string) SetupGitOption {
	return func(gs *GitSetup) {
		gs.Committer = &GitSetupPerson{Name: name, Email: email}
	}
}

// WithAuthor setups an author name and email to be used for the git operation.
func WithAuthor(name, email string) SetupGitOption {
	return func(gs *GitSetup) {
		gs.Author = &GitSetupPerson{Name: name, Email: email}
	}
}

// WithDefaultCommitter setups a committer with hardcoded values defined under CommitterName and CommitterEmail.
func WithDefaultCommitter() SetupGitOption {
	return WithCommitter(CommitterName, CommitterEmail)
}

// WithDefaultAuthor setups an author with hardcoded values defined under AuthorName and AuthorEmail.
func WithDefaultAuthor() SetupGitOption {
	return WithAuthor(AuthorName, AuthorEmail)
}

// ByScroogeMcDuck defines a default committer and an author of the commit.
func ByScroogeMcDuck() SetupGitOption {
	return func(gs *GitSetup) {
		if gs.Committer == nil {
			*gs = gs.Configure(WithDefaultCommitter())
		}
		if gs.Author == nil {
			*gs = gs.Configure(WithDefaultAuthor())
		}
	}
}

// WithRepoAt allows to pre-configure the path to the git repository.
func WithRepoAt(repoPath string) SetupGitOption {
	return func(gs *GitSetup) {
		gs.repoPath = repoPath
	}
}

// SetupGit creates and returns instance of the GitSetup.
func SetupGit(cfg config.Cfg, opts ...SetupGitOption) GitSetup {
	gs := GitSetup{Cfg: cfg}
	for _, opt := range opts {
		opt(&gs)
	}
	return gs
}

// Configure creates a copy of the setup and applies passed options on top of it and returns it back.
func (gs GitSetup) Configure(opts ...SetupGitOption) GitSetup {
	for _, opt := range opts {
		opt(&gs)
	}
	return gs
}

// Exec runs git command and returns its standard output, or fails.
func (gs GitSetup) Exec(t testing.TB, name string, args ...string) []byte {
	t.Helper()

	var gitArgs []string
	if gs.repoPath != "" {
		gitArgs = append(gitArgs, "-C", gs.repoPath)
	}
	gitArgs = append(gitArgs, name)
	gitArgs = append(gitArgs, args...)

	cmd := exec.Command(gs.Cfg.Git.BinPath, gitArgs...)
	cmd.Stdin = gs.Stdin
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, command.GitEnv...)
	cmd.Env = append(cmd.Env,
		"GIT_AUTHOR_DATE=1572776879 +0100",
		"GIT_COMMITTER_DATE=1572776879 +0100",
	)

	for who, person := range map[string]*GitSetupPerson{
		"AUTHOR":    gs.Author,
		"COMMITTER": gs.Committer,
	} {
		if person != nil {
			cmd.Env = append(cmd.Env, "GIT_"+who+"_NAME="+person.Name)
			cmd.Env = append(cmd.Env, "GIT_"+who+"_EMAIL="+person.Email)
		}
	}

	output, err := cmd.Output()
	if err != nil {
		execErr, ok := err.(*exec.ExitError)
		if ok {
			require.NoError(t, err, string(execErr.Stderr))
		} else {
			require.NoError(t, err, strings.Join(cmd.Args, " "))
		}
	}

	return output
}

func (gs GitSetup) requireRepoPath(t testing.TB) {
	require.NotEmpty(t, gs.repoPath)
}
