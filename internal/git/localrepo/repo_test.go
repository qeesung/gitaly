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

	gittest.TestRepository(t, cfg, func(ctx context.Context, tb testing.TB) (git.Repository, string) {
		tb.Helper()

		repoProto, repoPath := gittest.CreateRepository(ctx, tb, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		gitCmdFactory := gittest.NewCommandFactory(tb, cfg)
		catfileCache := catfile.NewCache(cfg)
		tb.Cleanup(catfileCache.Stop)
		return New(config.NewLocator(cfg), gitCmdFactory, catfileCache, repoProto), repoPath
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

	hashDependentSize := func(sha1Size, sha256Size int64) int64 {
		if gittest.ObjectHashIsSHA256() {
			return sha256Size
		}
		return sha1Size
	}

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
				repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				return repoProto
			},
			expectedUseBitmap: true,
		},
		{
			desc: "referenced commit",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "file", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
					gittest.WithBranch("main"),
				)

				return repoProto
			},
			expectedSize:      hashDependentSize(203, 230),
			expectedUseBitmap: true,
		},
		{
			desc: "unreferenced commit",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, repoPath,
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
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				rootCommitID := gittest.WriteCommit(t, cfg, repoPath,
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
			expectedSize:      hashDependentSize(439, 510),
			expectedUseBitmap: true,
		},
		{
			desc: "modification to blob after repack",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				rootCommitID := gittest.WriteCommit(t, cfg, repoPath,
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
			expectedSize:      hashDependentSize(398, 465),
			expectedUseBitmap: true,
		},
		{
			desc: "excluded single ref",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "1kbblob", Mode: "100644", Content: strings.Repeat("a", 1000)},
					),
					gittest.WithBranch("exclude-me"),
				)

				gittest.WriteCommit(t, cfg, repoPath,
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
			expectedSize:      hashDependentSize(217, 245),
			expectedUseBitmap: true,
		},
		{
			desc: "excluded everything",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, repoPath,
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
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				_, poolPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				for _, path := range []string{repoPath, poolPath} {
					gittest.WriteCommit(t, cfg, path,
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
			expectedSize: hashDependentSize(207, 234),
			// Even though we have an alternate, we should still use bitmap indices
			// given that we don't use `--not --alternate-refs`.
			expectedUseBitmap: true,
		},
		{
			desc: "exclude alternate with identical contents",
			setup: func(t *testing.T) *gitalypb.Repository {
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				_, poolPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				// We write the same object into both repositories, so we should
				// exclude it from our size calculations.
				for _, path := range []string{repoPath, poolPath} {
					gittest.WriteCommit(t, cfg, path,
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
				repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				_, poolPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(poolPath, "objects")),
					os.ModePerm,
				))

				for i, path := range []string{repoPath, poolPath} {
					// We first write one blob into the repo that is the same
					// across both repositories.
					rootCommitID := gittest.WriteCommit(t, cfg, path,
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
			expectedSize:      hashDependentSize(224, 268),
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
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	locator := config.NewLocator(cfg)

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := New(locator, gitCmdFactory, catfileCache, repoProto)

	expected, err := locator.TempDir(cfg.Storages[0].Name)
	require.NoError(t, err)
	require.NoDirExists(t, expected)

	tempPath, err := repo.StorageTempDir()
	require.NoError(t, err)
	require.DirExists(t, expected)
	require.Equal(t, expected, tempPath)
}

func TestRepo_ObjectHash(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	locator := config.NewLocator(cfg)

	outputFile := filepath.Join(testhelper.TempDir(t), "output")

	// We create an intercepting command factory that detects when we run our object hash
	// detection logic and, if so, writes a sentinel value into our output file. Like this we
	// can test how often the logic runs.
	gitCmdFactory := gittest.NewInterceptingCommandFactory(ctx, t, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(`#!/bin/sh
		( echo "$@" | grep --silent -- '--show-object-format' ) && echo detection-logic >>%q
		exec %q "$@"`, outputFile, execEnv.BinaryPath)
	})

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := New(locator, gitCmdFactory, catfileCache, repoProto)

	objectHash, err := repo.ObjectHash(ctx)
	require.NoError(t, err)
	require.Equal(t, gittest.DefaultObjectHash.EmptyTreeOID, objectHash.EmptyTreeOID)

	// We should see that the detection logic has been executed once.
	require.Equal(t, "detection-logic\n", string(testhelper.MustReadFile(t, outputFile)))

	// Verify that running this a second time continues to return the object hash alright
	// regardless of the cache.
	objectHash, err = repo.ObjectHash(ctx)
	require.NoError(t, err)
	require.Equal(t, gittest.DefaultObjectHash.EmptyTreeOID, objectHash.EmptyTreeOID)

	// But the detection logic should not have been executed a second time.
	require.Equal(t, "detection-logic\n", string(testhelper.MustReadFile(t, outputFile)))
}
