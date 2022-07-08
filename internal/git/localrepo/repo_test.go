package localrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestRepo(t *testing.T) {
	cfg := testcfg.Build(t)

	gittest.TestRepository(t, cfg, func(ctx context.Context, t testing.TB, seeded bool) (git.Repository, string) {
		t.Helper()

		var (
			pbRepo   *gitalypb.Repository
			repoPath string
		)

		if seeded {
			pbRepo, repoPath = gittest.CloneRepo(t, cfg, cfg.Storages[0])
		} else {
			pbRepo, repoPath = gittest.InitRepo(t, cfg, cfg.Storages[0])
		}

		gitCmdFactory := gittest.NewCommandFactory(t, cfg)
		catfileCache := catfile.NewCache(cfg)
		t.Cleanup(catfileCache.Stop)
		return New(config.NewLocator(cfg), gitCmdFactory, catfileCache, pbRepo), repoPath
	})
}

func TestSize(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	ctx := testhelper.Context(t)

	commandArgFile := filepath.Join(testhelper.TempDir(t), "args")
	interceptingFactory := gittest.NewInterceptingCommandFactory(ctx, t, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(`#!/bin/bash
			echo "$@" >%q
			exec %q "$@"
		`, commandArgFile, execEnv.BinaryPath)
	})

	testCases := []struct {
		desc              string
		setup             func(t *testing.T) *gitalypb.Repository
		opts              []RepoSizeOption
		expectedSize      int64
		expectedUseBitmap bool
	}{
		{
			desc:         "empty repository",
			expectedSize: 0,
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, _ := gittest.InitRepo(t, cfg, cfg.Storages[0])
				return repoProto
			},
			expectedUseBitmap: true,
		},
		{
			desc: "referenced commit",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
					gittest.WithBranch("main"),
				)

				return repoProto
			},
			expectedSize:      203,
			expectedUseBitmap: true,
		},
		{
			desc: "unreferenced commit",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
				)

				return repoProto
			},
			expectedSize:      0,
			expectedUseBitmap: true,
		},
		{
			desc: "modification to blob without repack",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				rootCommitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
				)

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(rootCommitID),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1001)},
					),
					gittest.WithMessage("modification"),
					gittest.WithBranch("main"),
				)

				return repoProto
			},
			expectedSize:      439,
			expectedUseBitmap: true,
		},
		{
			desc: "modification to blob after repack",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				rootCommitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
				)

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(rootCommitID),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1001)},
					),
					gittest.WithMessage("modification"),
					gittest.WithBranch("main"),
				)

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-a", "-d")

				return repoProto
			},
			expectedSize:      398,
			expectedUseBitmap: true,
		},
		{
			desc: "excluded single ref",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
					gittest.WithBranch("exclude-me"),
				)

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("x", 2000)},
					),
					gittest.WithBranch("include-me"),
				)

				return repoProto
			},
			opts: []RepoSizeOption{
				WithExcludeRefs("refs/heads/exclude-me"),
			},
			expectedSize:      217,
			expectedUseBitmap: true,
		},
		{
			desc: "excluded everything",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
					gittest.WithBranch("exclude-me"),
				)

				return repoProto
			},
			opts: []RepoSizeOption{
				WithExcludeRefs("refs/heads/*"),
			},
			expectedSize:      0,
			expectedUseBitmap: true,
		},
		{
			desc: "repo with alternate",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				_, poolPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				for _, path := range []string{repoPath, poolPath} {
					gittest.WriteCommit(t, cfg, path,
						gittest.WithParents(),
						gittest.WithTreeEntries(
							gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
						),
						gittest.WithBranch("main"),
					)
				}

				return repoProto
			},
			// While both repositories have the same contents, we should still return
			// the actual repository's size because we don't exclude the alternate.
			expectedSize: 207,
			// Even though we have an alternate, we should still use bitmap indices
			// given that we don't use `--not --alternate-refs`.
			expectedUseBitmap: true,
		},
		{
			desc: "exclude alternate with identical contents",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				_, poolPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				// We write the same object into both repositories, so we should
				// exclude it from our size calculations.
				for _, path := range []string{repoPath, poolPath} {
					gittest.WriteCommit(t, cfg, path,
						gittest.WithParents(),
						gittest.WithTreeEntries(
							gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
						),
						gittest.WithBranch("main"),
					)
				}

				return repoProto
			},
			opts: []RepoSizeOption{
				WithoutAlternates(),
			},
			expectedSize:      0,
			expectedUseBitmap: false,
		},
		{
			desc: "exclude alternate with additional contents",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				_, poolPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				for i, path := range []string{repoPath, poolPath} {
					// We first write one blob into the repo that is the same
					// across both repositories.
					rootCommitID := gittest.WriteCommit(t, cfg, path,
						gittest.WithParents(),
						gittest.WithTreeEntries(
							gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
						),
					)

					// But this time we also write a second commit into each of
					// the repositories that is not the same to simulate history
					// that has diverged.
					gittest.WriteCommit(t, cfg, path,
						gittest.WithParents(rootCommitID),
						gittest.WithTreeEntries(
							gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: fmt.Sprintf("%d", i)},
						),
						gittest.WithBranch("main"),
					)
				}

				return repoProto
			},
			opts: []RepoSizeOption{
				WithoutAlternates(),
			},
			expectedSize:      224,
			expectedUseBitmap: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.NoError(t, os.RemoveAll(commandArgFile))

			repoProto := tc.setup(t)
			repo := New(config.NewLocator(cfg), interceptingFactory, catfileCache, repoProto)

			size, err := repo.Size(ctx, tc.opts...)
			require.NoError(t, err)
			require.Equal(t, tc.expectedSize, size)

			commandArgs := text.ChompBytes(testhelper.MustReadFile(t, commandArgFile))
			if tc.expectedUseBitmap {
				require.Contains(t, commandArgs, "--use-bitmap-index")
			} else {
				require.NotContains(t, commandArgs, "--use-bitmap-index")
			}
		})
	}
}

func TestRepo_StorageTempDir(t *testing.T) {
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	locator := config.NewLocator(cfg)

	pbRepo, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := New(locator, gitCmdFactory, catfileCache, pbRepo)

	expected, err := locator.TempDir(cfg.Storages[0].Name)
	require.NoError(t, err)
	require.NoDirExists(t, expected)

	tempPath, err := repo.StorageTempDir()
	require.NoError(t, err)
	require.DirExists(t, expected)
	require.Equal(t, expected, tempPath)
}
