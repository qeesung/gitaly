package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestRepo_FetchRemote(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithSkipHooks())
	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()
	locator := config.NewLocator(cfg)

	_, remoteRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
	tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	initBareWithRemote := func(t *testing.T, remote string) (*Repo, string) {
		t.Helper()

		clientRepo, clientRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		cmd := gittest.NewCommand(t, cfg, "-C", clientRepoPath, "remote", "add", remote, remoteRepoPath)
		err := cmd.Run()
		if err != nil {
			require.NoError(t, err)
		}

		return New(locator, gitCmdFactory, catfileCache, clientRepo), clientRepoPath
	}

	t.Run("invalid name", func(t *testing.T) {
		repo := New(locator, gitCmdFactory, catfileCache, nil)

		err := repo.FetchRemote(ctx, " ", FetchOpts{})
		require.True(t, errors.Is(err, git.ErrInvalidArg))
		require.Contains(t, err.Error(), `"remoteName" is blank or empty`)
	})

	t.Run("unknown remote", func(t *testing.T) {
		repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repo := New(locator, gitCmdFactory, catfileCache, repoProto)
		var stderr bytes.Buffer
		err := repo.FetchRemote(ctx, "stub", FetchOpts{Stderr: &stderr})
		require.Error(t, err)
		require.Contains(t, stderr.String(), "'stub' does not appear to be a git repository")
	})

	t.Run("ok", func(t *testing.T) {
		repo, repoPath := initBareWithRemote(t, "origin")

		var stderr bytes.Buffer
		require.NoError(t, repo.FetchRemote(ctx, "origin", FetchOpts{Stderr: &stderr}))

		require.Empty(t, stderr.String(), "it should not produce output as it is called with --quiet flag by default")

		refs, err := repo.GetReferences(ctx)
		require.NoError(t, err)
		require.Contains(t, refs, git.Reference{Name: "refs/remotes/origin/main", Target: commitID.String()})
		require.Contains(t, refs, git.Reference{Name: "refs/tags/v1.0.0", Target: tagID.String()})

		fetchedCommitID, err := repo.ResolveRevision(ctx, git.Revision("refs/remotes/origin/main^{commit}"))
		require.NoError(t, err, "the object from remote should exists in local after fetch done")
		require.Equal(t, commitID, fetchedCommitID)

		require.NoFileExists(t, filepath.Join(repoPath, "FETCH_HEAD"))
	})

	t.Run("with env", func(t *testing.T) {
		testRepo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repo := New(locator, gitCmdFactory, catfileCache, testRepo)
		gittest.Exec(t, cfg, "-C", testRepoPath, "remote", "add", "source", remoteRepoPath)

		var stderr bytes.Buffer
		require.NoError(t, repo.FetchRemote(ctx, "source", FetchOpts{Stderr: &stderr, Env: []string{"GIT_TRACE=1"}}))
		require.Contains(t, stderr.String(), "trace: built-in: git fetch --no-write-fetch-head --quiet --atomic --end-of-options source")
	})

	t.Run("with disabled transactions", func(t *testing.T) {
		testRepo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repo := New(locator, gitCmdFactory, catfileCache, testRepo)
		gittest.Exec(t, cfg, "-C", testRepoPath, "remote", "add", "source", remoteRepoPath)

		var stderr bytes.Buffer
		require.NoError(t, repo.FetchRemote(ctx, "source", FetchOpts{
			Stderr:              &stderr,
			Env:                 []string{"GIT_TRACE=1"},
			DisableTransactions: true,
		}))
		require.Contains(t, stderr.String(), "trace: built-in: git fetch --no-write-fetch-head --quiet --end-of-options source")
	})

	t.Run("with globals", func(t *testing.T) {
		testRepo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repo := New(locator, gitCmdFactory, catfileCache, testRepo)
		gittest.Exec(t, cfg, "-C", testRepoPath, "remote", "add", "source", remoteRepoPath)

		require.NoError(t, repo.FetchRemote(ctx, "source", FetchOpts{}))

		// Write a commit into the remote's reference namespace that doesn't exist in the
		// remote and that would thus be pruned.
		gittest.WriteCommit(t, cfg, testRepoPath, gittest.WithReference("refs/remotes/source/markdown"))

		require.NoError(t, repo.FetchRemote(
			ctx,
			"source",
			FetchOpts{
				CommandOptions: []git.CmdOpt{
					git.WithConfig(git.ConfigPair{Key: "fetch.prune", Value: "true"}),
				},
			}),
		)

		contains, err := repo.HasRevision(ctx, git.Revision("refs/remotes/source/markdown"))
		require.NoError(t, err)
		require.False(t, contains, "remote tracking branch should be pruned as it no longer exists on the remote")
	})

	t.Run("with prune", func(t *testing.T) {
		testRepo, testRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repo := New(locator, gitCmdFactory, catfileCache, testRepo)

		gittest.Exec(t, cfg, "-C", testRepoPath, "remote", "add", "source", remoteRepoPath)
		require.NoError(t, repo.FetchRemote(ctx, "source", FetchOpts{}))
		// Write a commit into the remote's reference namespace that doesn't exist in the
		// remote and that would thus be pruned.
		gittest.WriteCommit(t, cfg, testRepoPath, gittest.WithReference("refs/remotes/source/markdown"))

		require.NoError(t, repo.FetchRemote(ctx, "source", FetchOpts{Prune: true}))

		contains, err := repo.HasRevision(ctx, git.Revision("refs/remotes/source/markdown"))
		require.NoError(t, err)
		require.False(t, contains, "remote tracking branch should be pruned as it no longer exists on the remote")
	})

	t.Run("with no tags", func(t *testing.T) {
		repo, testRepoPath := initBareWithRemote(t, "origin")

		tagsBefore := gittest.Exec(t, cfg, "-C", testRepoPath, "tag", "--list")
		require.Empty(t, tagsBefore)

		require.NoError(t, repo.FetchRemote(ctx, "origin", FetchOpts{Tags: FetchOptsTagsNone, Force: true}))

		tagsAfter := gittest.Exec(t, cfg, "-C", testRepoPath, "tag", "--list")
		require.Empty(t, tagsAfter)

		containsBranches, err := repo.HasRevision(ctx, git.Revision("'test'"))
		require.NoError(t, err)
		require.False(t, containsBranches)

		containsTags, err := repo.HasRevision(ctx, git.Revision("v1.1.0"))
		require.NoError(t, err)
		require.False(t, containsTags)
	})

	t.Run("with invalid remote", func(t *testing.T) {
		repo, _ := initBareWithRemote(t, "origin")

		err := repo.FetchRemote(ctx, "doesnotexist", FetchOpts{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "fatal: 'doesnotexist' does not appear to be a git repository")
		require.IsType(t, err, ErrFetchFailed{})
	})

	t.Run("generates reverse index", func(t *testing.T) {
		repo, repoPath := initBareWithRemote(t, "origin")

		// The repository has no objects yet, so there shouldn't be any packfile either.
		packfiles, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.pack"))
		require.NoError(t, err)
		require.Empty(t, packfiles)

		// Same goes for reverse indices, naturally.
		reverseIndices, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.rev"))
		require.NoError(t, err)
		require.Empty(t, reverseIndices)

		require.NoError(t, repo.FetchRemote(ctx, "origin", FetchOpts{}))

		// After the fetch we should end up with a single packfile.
		packfiles, err = filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.pack"))
		require.NoError(t, err)
		require.Len(t, packfiles, 1)

		// And furthermore, that packfile should have a reverse index.
		reverseIndices, err = filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.rev"))
		require.NoError(t, err)
		require.Len(t, reverseIndices, 1)
	})
}

// captureGitSSHCommand creates a new intercepting command factory which captures the
// GIT_SSH_COMMAND environment variable. The returned function can be used to read the variable's
// value.
func captureGitSSHCommand(ctx context.Context, t testing.TB, cfg config.Cfg) (git.CommandFactory, func() ([]byte, error)) {
	envPath := filepath.Join(testhelper.TempDir(t), "GIT_SSH_PATH")

	gitCmdFactory := gittest.NewInterceptingCommandFactory(ctx, t, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(
			`#!/usr/bin/env bash
			if test -z "${GIT_SSH_COMMAND+x}"
			then
				rm -f %q
			else
				echo -n "$GIT_SSH_COMMAND" >%q
			fi
			%q "$@"
		`, envPath, envPath, execEnv.BinaryPath)
	})

	return gitCmdFactory, func() ([]byte, error) {
		return os.ReadFile(envPath)
	}
}

func TestRepo_Push(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)

	gitCmdFactory, readSSHCommand := captureGitSSHCommand(ctx, t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	locator := config.NewLocator(cfg)

	sourceRepoProto, sourceRepoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	sourceRepo := New(locator, gitCmdFactory, catfileCache, sourceRepoProto)
	gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("master"))
	gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("feature"))

	setupPushRepo := func(t testing.TB) (*Repo, string, []git.ConfigPair) {
		repoProto, repopath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		return New(locator, gitCmdFactory, catfileCache, repoProto), repopath, nil
	}

	setupDivergedRepo := func(t testing.TB) (*Repo, string, []git.ConfigPair) {
		repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := New(locator, gitCmdFactory, catfileCache, repoProto)

		// set up master as a diverging ref in push repo
		sourceMaster, err := sourceRepo.GetReference(ctx, "refs/heads/master")
		require.NoError(t, err)

		require.NoError(t, sourceRepo.Push(ctx, repoPath, []string{"refs/*"}, PushOptions{}))
		divergedMaster := gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithBranch("master"),
			gittest.WithParents(git.ObjectID(sourceMaster.Target)),
		)

		master, err := repo.GetReference(ctx, "refs/heads/master")
		require.NoError(t, err)
		require.Equal(t, master.Target, divergedMaster.String())

		return repo, repoPath, nil
	}

	for _, tc := range []struct {
		desc           string
		setupPushRepo  func(testing.TB) (*Repo, string, []git.ConfigPair)
		config         []git.ConfigPair
		sshCommand     string
		force          bool
		refspecs       []string
		errorMessage   string
		expectedFilter []string
	}{
		{
			desc:          "refspecs must be specified",
			setupPushRepo: setupPushRepo,
			errorMessage:  "refspecs to push must be explicitly specified",
		},
		{
			desc:           "push two refs",
			setupPushRepo:  setupPushRepo,
			refspecs:       []string{"refs/heads/master", "refs/heads/feature"},
			expectedFilter: []string{"refs/heads/master", "refs/heads/feature"},
		},
		{
			desc:           "push with custom ssh command",
			setupPushRepo:  setupPushRepo,
			sshCommand:     "custom --ssh-command",
			refspecs:       []string{"refs/heads/master"},
			expectedFilter: []string{"refs/heads/master"},
		},
		{
			desc:          "doesn't force push over diverged refs with Force unset",
			refspecs:      []string{"refs/heads/master"},
			setupPushRepo: setupDivergedRepo,
			errorMessage:  "Updates were rejected because the remote contains work that you do",
		},
		{
			desc:          "force pushes over diverged refs with Force set",
			refspecs:      []string{"refs/heads/master"},
			force:         true,
			setupPushRepo: setupDivergedRepo,
		},
		{
			desc:          "push all refs",
			setupPushRepo: setupPushRepo,
			refspecs:      []string{"refs/*"},
		},
		{
			desc:          "push empty refspec",
			setupPushRepo: setupPushRepo,
			refspecs:      []string{""},
			errorMessage:  `git push: exit status 128, stderr: "fatal: invalid refspec ''\n"`,
		},
		{
			desc: "invalid remote",
			setupPushRepo: func(t testing.TB) (*Repo, string, []git.ConfigPair) {
				repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				return New(locator, gitCmdFactory, catfileCache, repoProto), "", nil
			},
			refspecs:     []string{"refs/heads/master"},
			errorMessage: `git push: exit status 128, stderr: "fatal: no path specified; see 'git help pull' for valid url syntax\n"`,
		},
		{
			desc: "in-memory remote",
			setupPushRepo: func(testing.TB) (*Repo, string, []git.ConfigPair) {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				return New(locator, gitCmdFactory, catfileCache, repoProto), "inmemory", []git.ConfigPair{
					{Key: "remote.inmemory.url", Value: repoPath},
				}
			},
			refspecs:       []string{"refs/heads/master"},
			expectedFilter: []string{"refs/heads/master"},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			pushRepo, remote, remoteConfig := tc.setupPushRepo(t)

			err := sourceRepo.Push(ctx, remote, tc.refspecs, PushOptions{
				SSHCommand: tc.sshCommand,
				Force:      tc.force,
				Config:     remoteConfig,
			})
			if tc.errorMessage != "" {
				require.Contains(t, err.Error(), tc.errorMessage)
				return
			}
			require.NoError(t, err)

			gitSSHCommand, err := readSSHCommand()
			if !os.IsNotExist(err) {
				require.NoError(t, err)
			}

			require.Equal(t, tc.sshCommand, string(gitSSHCommand))

			actual, err := pushRepo.GetReferences(ctx)
			require.NoError(t, err)

			expected, err := sourceRepo.GetReferences(ctx, tc.expectedFilter...)
			require.NoError(t, err)

			require.Equal(t, expected, actual)
		})
	}
}
