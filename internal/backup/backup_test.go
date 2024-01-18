package backup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func TestManager_RemoveRepository(t *testing.T) {
	if testhelper.IsPraefectEnabled() {
		t.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
	}

	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())

	pool := client.NewPool()
	defer testhelper.MustClose(t, pool)

	backupRoot := testhelper.TempDir(t)
	sink := backup.NewFilesystemSink(backupRoot)
	defer testhelper.MustClose(t, sink)

	locator, err := backup.ResolveLocator("pointer", sink)
	require.NoError(t, err)

	fsBackup := backup.NewManager(sink, locator, pool)
	err = fsBackup.RemoveRepository(ctx, &backup.RemoveRepositoryRequest{
		Server: storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
		Repo:   repo,
	})
	require.NoError(t, err)
	require.NoDirExists(t, repoPath)

	// With an invalid repository
	err = fsBackup.RemoveRepository(ctx, &backup.RemoveRepositoryRequest{
		Server: storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
		Repo:   &gitalypb.Repository{StorageName: "nonexistent", RelativePath: "nonexistent"},
	})

	require.EqualError(t, err, "remove repo: remove: rpc error: code = InvalidArgument desc = storage name not found")
}

func TestManager_ListRepositories(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc  string
		repos map[string][]*gitalypb.Repository
	}{
		{
			desc:  "no repos",
			repos: make(map[string][]*gitalypb.Repository),
		},
		{
			desc: "repos in a single storage",
			repos: map[string][]*gitalypb.Repository{
				"storage-1": {
					{RelativePath: "a", StorageName: "storage-1"},
					{RelativePath: "b", StorageName: "storage-1"},
				},
			},
		},
		{
			desc: "repos in multiple storages",
			repos: map[string][]*gitalypb.Repository{
				"storage-1": {
					{RelativePath: "a", StorageName: "storage-1"},
					{RelativePath: "b", StorageName: "storage-1"},
				},
				"storage-2": {
					{RelativePath: "c", StorageName: "storage-2"},
					{RelativePath: "d", StorageName: "storage-2"},
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if testhelper.IsPraefectEnabled() {
				t.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
			}

			var storages []string
			for storageName := range tc.repos {
				storages = append(storages, storageName)
			}

			// We don't really need a "default" storage, but this makes initialisation cleaner since
			// WithStorages() takes at least one argument.
			cfg := testcfg.Build(t, testcfg.WithStorages("default", storages...))
			cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

			ctx := testhelper.Context(t)

			for storageName, repos := range tc.repos {
				for _, repo := range repos {
					storagePath, ok := cfg.StoragePath(storageName)
					require.True(t, ok)

					_, _ = gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
						SkipCreationViaService: true,
						RelativePath:           repo.RelativePath,
						Storage:                config.Storage{Name: storageName, Path: storagePath},
					})
				}
			}

			pool := client.NewPool()
			defer testhelper.MustClose(t, pool)

			backupRoot := testhelper.TempDir(t)
			sink := backup.NewFilesystemSink(backupRoot)
			defer testhelper.MustClose(t, sink)

			locator, err := backup.ResolveLocator("pointer", sink)
			require.NoError(t, err)

			fsBackup := backup.NewManager(sink, locator, pool)

			for storageName, repos := range tc.repos {
				actualRepos, err := fsBackup.ListRepositories(ctx, &backup.ListRepositoriesRequest{
					Server:      storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
					StorageName: storageName,
				})

				require.NoError(t, err)
				require.EqualValues(t, repos, actualRepos)
			}
		})
	}
}

func TestManager_Create(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	cfg := testcfg.Build(t)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	ctx := testhelper.Context(t)
	repoCounter := counter.NewRepositoryCounter(cfg.Storages)

	for _, managerTC := range []struct {
		desc  string
		setup func(t testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager
	}{
		{
			desc: "RPC manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				pool := client.NewPool()
				tb.Cleanup(func() {
					testhelper.MustClose(tb, pool)
				})

				return backup.NewManager(sink, locator, pool)
			},
		},
		{
			desc: "Local manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				if testhelper.IsPraefectEnabled() {
					tb.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
				}

				storageLocator := config.NewLocator(cfg)
				gitCmdFactory := gittest.NewCommandFactory(tb, cfg)
				catfileCache := catfile.NewCache(cfg)
				tb.Cleanup(catfileCache.Stop)
				txManager := transaction.NewTrackingManager()

				return backup.NewManagerLocal(sink, testhelper.SharedLogger(t), locator, storageLocator, gitCmdFactory, catfileCache, txManager, repoCounter)
			},
		},
	} {

		type setupData struct {
			repo           *gitalypb.Repository
			repoPath       string
			expectedBackup *backup.Backup
		}

		for _, tc := range []struct {
			desc               string
			setup              func(tb testing.TB, vanityRepo storage.Repository) setupData
			createsRefList     bool
			createsBundle      bool
			createsCustomHooks bool
			err                error
		}{
			{
				desc: "no hooks",
				setup: func(tb testing.TB, vanityRepo storage.Repository) setupData {
					repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

					return setupData{
						repo:     repo,
						repoPath: repoPath,
						expectedBackup: &backup.Backup{
							ID:            backupID,
							Repository:    vanityRepo,
							ObjectFormat:  gittest.DefaultObjectHash.Format,
							HeadReference: git.DefaultRef.String(),
							Steps: []backup.Step{
								{
									BundlePath:      joinBackupPath(t, "", vanityRepo, backupID, "001.bundle"),
									RefPath:         joinBackupPath(t, "", vanityRepo, backupID, "001.refs"),
									CustomHooksPath: joinBackupPath(t, "", vanityRepo, backupID, "001.custom_hooks.tar"),
								},
							},
						},
					}
				},
				createsRefList:     true,
				createsBundle:      true,
				createsCustomHooks: false,
			},
			{
				desc: "hooks",
				setup: func(tb testing.TB, vanityRepo storage.Repository) setupData {
					repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
					require.NoError(tb, os.Mkdir(filepath.Join(repoPath, "custom_hooks"), perm.PublicDir))
					require.NoError(tb, os.WriteFile(filepath.Join(repoPath, "custom_hooks/pre-commit.sample"), []byte("Some hooks"), perm.PublicFile))

					return setupData{
						repo:     repo,
						repoPath: repoPath,
						expectedBackup: &backup.Backup{
							ID:            backupID,
							Repository:    vanityRepo,
							ObjectFormat:  gittest.DefaultObjectHash.Format,
							HeadReference: git.DefaultRef.String(),
							Steps: []backup.Step{
								{
									BundlePath:      joinBackupPath(t, "", vanityRepo, backupID, "001.bundle"),
									RefPath:         joinBackupPath(t, "", vanityRepo, backupID, "001.refs"),
									CustomHooksPath: joinBackupPath(t, "", vanityRepo, backupID, "001.custom_hooks.tar"),
								},
							},
						},
					}
				},
				createsRefList:     true,
				createsBundle:      true,
				createsCustomHooks: true,
			},
			{
				desc: "empty repo",
				setup: func(tb testing.TB, vanityRepo storage.Repository) setupData {
					emptyRepo, repoPath := gittest.CreateRepository(tb, ctx, cfg)

					return setupData{
						repo:     emptyRepo,
						repoPath: repoPath,
						expectedBackup: &backup.Backup{
							ID:            backupID,
							Repository:    vanityRepo,
							ObjectFormat:  gittest.DefaultObjectHash.Format,
							HeadReference: git.DefaultRef.String(),
							Steps: []backup.Step{
								{
									BundlePath:      joinBackupPath(t, "", vanityRepo, backupID, "001.bundle"),
									RefPath:         joinBackupPath(t, "", vanityRepo, backupID, "001.refs"),
									CustomHooksPath: joinBackupPath(t, "", vanityRepo, backupID, "001.custom_hooks.tar"),
								},
							},
						},
					}
				},
				createsRefList:     true,
				createsBundle:      false,
				createsCustomHooks: false,
			},
			{
				desc: "nonexistent repo",
				setup: func(tb testing.TB, vanityRepo storage.Repository) setupData {
					emptyRepo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					nonexistentRepo := proto.Clone(emptyRepo).(*gitalypb.Repository)
					nonexistentRepo.RelativePath = gittest.NewRepositoryName(t)

					return setupData{
						repo:     nonexistentRepo,
						repoPath: repoPath,
					}
				},
				createsRefList:     false,
				createsBundle:      false,
				createsCustomHooks: false,
				err:                fmt.Errorf("manager: repository not found: %w", backup.ErrSkipped),
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				backupRoot := testhelper.TempDir(t)
				vanityRepo := &gitalypb.Repository{
					RelativePath: "some/path.git",
					StorageName:  "some_storage",
				}

				data := tc.setup(t, vanityRepo)

				manifestPath := filepath.Join(backupRoot, "manifests", vanityRepo.StorageName, vanityRepo.RelativePath, backupID+".toml")
				refsPath := joinBackupPath(t, backupRoot, vanityRepo, backupID, "001.refs")
				bundlePath := joinBackupPath(t, backupRoot, vanityRepo, backupID, "001.bundle")
				customHooksPath := joinBackupPath(t, backupRoot, vanityRepo, backupID, "001.custom_hooks.tar")

				sink := backup.NewFilesystemSink(backupRoot)
				defer testhelper.MustClose(t, sink)

				locator, err := backup.ResolveLocator("pointer", sink)
				require.NoError(t, err)

				fsBackup := managerTC.setup(t, sink, locator)
				err = fsBackup.Create(ctx, &backup.CreateRequest{
					Server:           storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
					Repository:       data.repo,
					VanityRepository: vanityRepo,
					BackupID:         backupID,
				})
				if tc.err == nil {
					require.NoError(t, err)
				} else {
					require.Equal(t, tc.err, err)
				}

				if tc.createsBundle {
					require.FileExists(t, refsPath)
					require.FileExists(t, bundlePath)

					dirInfo, err := os.Stat(filepath.Dir(bundlePath))
					require.NoError(t, err)
					require.Equal(t, perm.PrivateDir, dirInfo.Mode().Perm(), "expecting restricted directory permissions")

					bundleInfo, err := os.Stat(bundlePath)
					require.NoError(t, err)
					require.Equal(t, perm.PrivateFile, bundleInfo.Mode().Perm(), "expecting restricted file permissions")

					output := gittest.Exec(t, cfg, "-C", data.repoPath, "bundle", "verify", bundlePath)
					require.Contains(t, string(output), "The bundle records a complete history")

					expectedRefs := gittest.Exec(t, cfg, "-C", data.repoPath, "show-ref", "--head")
					actualRefs := testhelper.MustReadFile(t, refsPath)
					require.Equal(t, string(expectedRefs), string(actualRefs))
				} else {
					require.NoFileExists(t, bundlePath)
				}

				if tc.createsRefList {
					require.FileExists(t, refsPath)
				} else {
					require.NoFileExists(t, refsPath)
				}

				if tc.createsBundle || tc.createsRefList {
					require.FileExists(t, manifestPath)
				} else {
					require.NoFileExists(t, manifestPath)
				}

				if tc.createsCustomHooks {
					require.FileExists(t, customHooksPath)
				} else {
					require.NoFileExists(t, customHooksPath)
				}

				if data.expectedBackup == nil {
					_, err := locator.Find(ctx, vanityRepo, backupID)
					require.ErrorIs(t, err, backup.ErrDoesntExist)
				} else {
					backup, err := locator.Find(ctx, vanityRepo, backupID)
					require.NoError(t, err)
					require.Equal(t, data.expectedBackup, backup)
				}
			})
		}
	}
}

func TestManager_Create_incremental(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	cfg := testcfg.Build(t)

	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)
	ctx := testhelper.Context(t)
	repoCounter := counter.NewRepositoryCounter(cfg.Storages)

	for _, managerTC := range []struct {
		desc  string
		setup func(t testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager
	}{
		{
			desc: "RPC manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				pool := client.NewPool()
				tb.Cleanup(func() {
					testhelper.MustClose(tb, pool)
				})

				return backup.NewManager(sink, locator, pool)
			},
		},
		{
			desc: "Local manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				if testhelper.IsPraefectEnabled() {
					tb.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
				}

				storageLocator := config.NewLocator(cfg)
				gitCmdFactory := gittest.NewCommandFactory(tb, cfg)
				catfileCache := catfile.NewCache(cfg)
				tb.Cleanup(catfileCache.Stop)
				txManager := transaction.NewTrackingManager()

				return backup.NewManagerLocal(sink, testhelper.SharedLogger(t), locator, storageLocator, gitCmdFactory, catfileCache, txManager, repoCounter)
			},
		},
	} {
		for _, tc := range []struct {
			desc              string
			setup             func(t testing.TB, backupRoot string) (*gitalypb.Repository, string)
			expectedIncrement string
			expectedErr       error
		}{
			{
				desc: "no previous backup",
				setup: func(tb testing.TB, backupRoot string) (*gitalypb.Repository, string) {
					repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
					return repo, repoPath
				},
				expectedIncrement: "001",
			},
			{
				desc: "previous backup, no updates",
				setup: func(tb testing.TB, backupRoot string) (*gitalypb.Repository, string) {
					repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

					backupRepoPath := joinBackupPath(tb, backupRoot, repo)
					backupPath := filepath.Join(backupRepoPath, backupID)
					bundlePath := filepath.Join(backupPath, "001.bundle")
					refsPath := filepath.Join(backupPath, "001.refs")

					require.NoError(tb, os.MkdirAll(backupPath, perm.PublicDir))
					gittest.Exec(tb, cfg, "-C", repoPath, "bundle", "create", bundlePath, "--all")

					refs := gittest.Exec(tb, cfg, "-C", repoPath, "show-ref", "--head")
					require.NoError(tb, os.WriteFile(refsPath, refs, perm.PublicFile))

					require.NoError(tb, os.WriteFile(filepath.Join(backupRepoPath, "LATEST"), []byte(backupID), perm.PublicFile))
					require.NoError(tb, os.WriteFile(filepath.Join(backupPath, "LATEST"), []byte("001"), perm.PublicFile))

					return repo, repoPath
				},
				expectedErr: fmt.Errorf("manager: %w", fmt.Errorf("write bundle: %w: no changes to bundle", backup.ErrSkipped)),
			},
			{
				desc: "previous backup, updates",
				setup: func(tb testing.TB, backupRoot string) (*gitalypb.Repository, string) {
					repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)
					commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

					backupRepoPath := joinBackupPath(tb, backupRoot, repo)
					backupPath := filepath.Join(backupRepoPath, backupID)
					bundlePath := filepath.Join(backupPath, "001.bundle")
					refsPath := filepath.Join(backupPath, "001.refs")

					require.NoError(tb, os.MkdirAll(backupPath, perm.PublicDir))
					gittest.Exec(tb, cfg, "-C", repoPath, "bundle", "create", bundlePath, "--all")

					refs := gittest.Exec(tb, cfg, "-C", repoPath, "show-ref", "--head")
					require.NoError(tb, os.WriteFile(refsPath, refs, perm.PublicFile))

					require.NoError(tb, os.WriteFile(filepath.Join(backupRepoPath, "LATEST"), []byte(backupID), perm.PublicFile))
					require.NoError(tb, os.WriteFile(filepath.Join(backupPath, "LATEST"), []byte("001"), perm.PublicFile))

					gittest.WriteCommit(tb, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithParents(commitID))

					return repo, repoPath
				},
				expectedIncrement: "002",
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				backupRoot := testhelper.TempDir(t)
				repo, repoPath := tc.setup(t, backupRoot)

				refsPath := joinBackupPath(t, backupRoot, repo, backupID, tc.expectedIncrement+".refs")
				bundlePath := joinBackupPath(t, backupRoot, repo, backupID, tc.expectedIncrement+".bundle")

				sink := backup.NewFilesystemSink(backupRoot)
				defer testhelper.MustClose(t, sink)

				locator, err := backup.ResolveLocator("pointer", sink)
				require.NoError(t, err)

				fsBackup := managerTC.setup(t, sink, locator)
				err = fsBackup.Create(ctx, &backup.CreateRequest{
					Server:      storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
					Repository:  repo,
					Incremental: true,
					BackupID:    backupID,
				})
				if tc.expectedErr == nil {
					require.NoError(t, err)
				} else {
					require.Equal(t, tc.expectedErr, err)
					return
				}

				require.FileExists(t, refsPath)
				require.FileExists(t, bundlePath)

				expectedRefs := gittest.Exec(t, cfg, "-C", repoPath, "show-ref", "--head")
				actualRefs := testhelper.MustReadFile(t, refsPath)
				require.Equal(t, string(expectedRefs), string(actualRefs))
			})
		}
	}
}

func TestManager_Restore_latest(t *testing.T) {
	gittest.SkipWithSHA256(t)

	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)
	repoCounter := counter.NewRepositoryCounter(cfg.Storages)

	for _, managerTC := range []struct {
		desc  string
		setup func(t testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager
	}{
		{
			desc: "RPC manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				pool := client.NewPool()
				tb.Cleanup(func() {
					testhelper.MustClose(tb, pool)
				})

				return backup.NewManager(sink, locator, pool)
			},
		},
		{
			desc: "Local manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				if testhelper.IsPraefectEnabled() {
					tb.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
				}

				storageLocator := config.NewLocator(cfg)
				gitCmdFactory := gittest.NewCommandFactory(tb, cfg)
				catfileCache := catfile.NewCache(cfg)
				tb.Cleanup(catfileCache.Stop)
				txManager := transaction.NewTrackingManager()

				return backup.NewManagerLocal(sink, testhelper.SharedLogger(t), locator, storageLocator, gitCmdFactory, catfileCache, txManager, repoCounter)
			},
		},
	} {
		managerTC := managerTC

		t.Run(managerTC.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)

			cc, err := client.Dial(ctx, cfg.SocketPath)
			require.NoError(t, err)
			defer testhelper.MustClose(t, cc)

			repoClient := gitalypb.NewRepositoryServiceClient(cc)

			_, repoPath := gittest.CreateRepository(t, ctx, cfg)
			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
			gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())
			repoChecksum := gittest.ChecksumRepo(t, cfg, repoPath)
			repoBundle := gittest.BundleRepo(t, cfg, repoPath, "-")
			repoRefs := gittest.Exec(t, cfg, "-C", repoPath, "show-ref", "--head")

			backupRoot := testhelper.TempDir(t)

			for _, tc := range []struct {
				desc          string
				locators      []string
				setup         func(tb testing.TB) (*gitalypb.Repository, *git.Checksum)
				alwaysCreate  bool
				expectExists  bool
				expectedPaths []string
				expectedErrAs error
			}{
				{
					desc:     "existing repo, without hooks",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							relativePath + ".bundle": repoBundle,
							relativePath + ".refs":   repoRefs,
						})

						return repo, repoChecksum
					},
					expectExists: true,
				},
				{
					desc:     "existing repo, with hooks",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						customHooksPath := filepath.Join(backupRoot, relativePath, "custom_hooks.tar")
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							relativePath + ".bundle": repoBundle,
							relativePath + ".refs":   repoRefs,
						})

						require.NoError(tb, os.MkdirAll(filepath.Join(backupRoot, relativePath), perm.PublicDir))
						testhelper.CopyFile(tb, mustCreateCustomHooksArchive(t, ctx), customHooksPath)

						return repo, repoChecksum
					},
					expectedPaths: []string{
						"custom_hooks/pre-commit.sample",
						"custom_hooks/prepare-commit-msg.sample",
						"custom_hooks/pre-push.sample",
					},
					expectExists: true,
				},
				{
					desc:     "missing backup",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)
						return repo, nil
					},
					expectedErrAs: backup.ErrSkipped,
				},
				{
					desc:     "missing backup, always create",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)
						return repo, new(git.Checksum)
					},
					alwaysCreate: true,
					expectExists: true,
				},
				{
					desc:     "empty backup",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							relativePath + ".refs": "",
						})

						return repo, new(git.Checksum)
					},
					expectExists: true,
				},
				{
					desc:     "empty backup, always create",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							relativePath + ".refs": "",
						})

						return repo, new(git.Checksum)
					},
					alwaysCreate: true,
					expectExists: true,
				},
				{
					desc:     "nonexistent repo",
					locators: []string{"legacy", "pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo := &gitalypb.Repository{
							StorageName:  "default",
							RelativePath: gittest.NewRepositoryName(tb),
						}

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							relativePath + ".bundle": repoBundle,
							relativePath + ".refs":   repoRefs,
						})

						return repo, repoChecksum
					},
					expectExists: true,
				},
				{
					desc:     "single incremental",
					locators: []string{"pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						const backupID = "abc123"
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join(relativePath, "LATEST"):               backupID,
							filepath.Join(relativePath, backupID, "LATEST"):     "001",
							filepath.Join(relativePath, backupID, "001.bundle"): repoBundle,
							filepath.Join(relativePath, backupID, "001.refs"):   repoRefs,
						})

						return repo, repoChecksum
					},
					expectExists: true,
				},
				{
					desc:     "single incremental, empty backup",
					locators: []string{"pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						const backupID = "abc123"
						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join(relativePath, "LATEST"):             backupID,
							filepath.Join(relativePath, backupID, "LATEST"):   "001",
							filepath.Join(relativePath, backupID, "001.refs"): "",
						})

						return repo, new(git.Checksum)
					},
					expectExists: true,
				},
				{
					desc:     "many incrementals",
					locators: []string{"pointer"},
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						const backupID = "abc123"

						_, expectedRepoPath := gittest.CreateRepository(t, ctx, cfg)

						repo, _ := gittest.CreateRepository(t, ctx, cfg)

						root := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
						)
						master1 := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
							gittest.WithParents(root),
						)
						other := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("other"),
							gittest.WithParents(root),
						)
						gittest.Exec(tb, cfg, "-C", expectedRepoPath, "symbolic-ref", "HEAD", "refs/heads/master")
						bundle1 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "bundle", "create", "-",
							"HEAD",
							"refs/heads/master",
							"refs/heads/other",
						)
						refs1 := gittest.Exec(t, cfg, "-C", expectedRepoPath, "show-ref", "--head")

						master2 := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
							gittest.WithParents(master1),
						)
						bundle2 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "bundle", "create", "-",
							"HEAD",
							"^"+master1.String(),
							"^"+other.String(),
							"refs/heads/master",
							"refs/heads/other",
						)
						refs2 := gittest.Exec(t, cfg, "-C", expectedRepoPath, "show-ref", "--head")

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join(relativePath, "LATEST"):               backupID,
							filepath.Join(relativePath, backupID, "LATEST"):     "002",
							filepath.Join(relativePath, backupID, "001.bundle"): bundle1,
							filepath.Join(relativePath, backupID, "002.bundle"): bundle2,
							filepath.Join(relativePath, backupID, "001.refs"):   refs1,
							filepath.Join(relativePath, backupID, "002.refs"):   refs2,
						})

						checksum := new(git.Checksum)
						checksum.Add(git.NewReference("HEAD", master2))
						checksum.Add(git.NewReference("refs/heads/master", master2))
						checksum.Add(git.NewReference("refs/heads/other", other))

						return repo, checksum
					},
					expectExists: true,
				},
			} {
				t.Run(tc.desc, func(t *testing.T) {
					require.GreaterOrEqual(t, len(tc.locators), 1, "each test case must specify a locator")

					for _, locatorName := range tc.locators {
						t.Run(locatorName, func(t *testing.T) {
							repo, expectedChecksum := tc.setup(t)

							sink := backup.NewFilesystemSink(backupRoot)
							defer testhelper.MustClose(t, sink)

							locator, err := backup.ResolveLocator(locatorName, sink)
							require.NoError(t, err)

							fsBackup := managerTC.setup(t, sink, locator)
							err = fsBackup.Restore(ctx, &backup.RestoreRequest{
								Server:           storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
								Repository:       repo,
								VanityRepository: repo,
								AlwaysCreate:     tc.alwaysCreate,
								BackupID:         "",
							})
							if tc.expectedErrAs != nil {
								require.ErrorAs(t, err, &tc.expectedErrAs)
							} else {
								require.NoError(t, err)
							}

							exists, err := repoClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
								Repository: repo,
							})
							require.NoError(t, err)
							require.Equal(t, tc.expectExists, exists.Exists, "repository exists")

							if expectedChecksum != nil {
								checksum, err := repoClient.CalculateChecksum(ctx, &gitalypb.CalculateChecksumRequest{
									Repository: repo,
								})
								require.NoError(t, err)

								require.Equal(t, expectedChecksum.String(), checksum.GetChecksum())
							}

							if len(tc.expectedPaths) > 0 {
								// Restore has to use the rewritten path as the relative path due to the test creating
								// the repository through Praefect. In order to get to the correct disk paths, we need
								// to get the replica path of the rewritten repository.
								repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
								for _, p := range tc.expectedPaths {
									require.FileExists(t, filepath.Join(repoPath, p))
								}
							}
						})
					}
				})
			}
		})
	}
}

func TestManager_Restore_specific(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)
	repoCounter := counter.NewRepositoryCounter(cfg.Storages)

	for _, managerTC := range []struct {
		desc  string
		setup func(t testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager
	}{
		{
			desc: "RPC manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				pool := client.NewPool()
				tb.Cleanup(func() {
					testhelper.MustClose(tb, pool)
				})

				return backup.NewManager(sink, locator, pool)
			},
		},
		{
			desc: "Local manager",
			setup: func(tb testing.TB, sink backup.Sink, locator backup.Locator) *backup.Manager {
				if testhelper.IsPraefectEnabled() {
					tb.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
				}

				storageLocator := config.NewLocator(cfg)
				gitCmdFactory := gittest.NewCommandFactory(tb, cfg)
				catfileCache := catfile.NewCache(cfg)
				tb.Cleanup(catfileCache.Stop)
				txManager := transaction.NewTrackingManager()

				return backup.NewManagerLocal(sink, testhelper.SharedLogger(t), locator, storageLocator, gitCmdFactory, catfileCache, txManager, repoCounter)
			},
		},
	} {
		managerTC := managerTC

		t.Run(managerTC.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)

			cc, err := client.Dial(ctx, cfg.SocketPath)
			require.NoError(t, err)
			defer testhelper.MustClose(t, cc)

			repoClient := gitalypb.NewRepositoryServiceClient(cc)

			_, repoPath := gittest.CreateRepository(t, ctx, cfg)
			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
			gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())
			repoChecksum := gittest.ChecksumRepo(t, cfg, repoPath)
			repoBundle := gittest.BundleRepo(t, cfg, repoPath, "-")
			repoRefs := gittest.Exec(t, cfg, "-C", repoPath, "show-ref", "--head")

			backupRoot := testhelper.TempDir(t)

			for _, tc := range []struct {
				desc                  string
				setup                 func(tb testing.TB) (*gitalypb.Repository, *git.Checksum)
				alwaysCreate          bool
				expectExists          bool
				expectedPaths         []string
				expectedErrAs         error
				expectedHeadReference git.ReferenceName
			}{
				{
					desc: "missing backup",
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(tb, ctx, cfg)

						return repo, nil
					},
					expectedErrAs: backup.ErrSkipped,
					expectExists:  true,
				},
				{
					desc: "single incremental",
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						gittest.SkipWithSHA256(tb) // sha256 only works with manifest files

						repo, _ := gittest.CreateRepository(tb, ctx, cfg)

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join(relativePath, "LATEST"):               backupID,
							filepath.Join(relativePath, backupID, "LATEST"):     "001",
							filepath.Join(relativePath, backupID, "001.bundle"): repoBundle,
							filepath.Join(relativePath, backupID, "001.refs"):   repoRefs,
						})

						return repo, repoChecksum
					},
					expectExists: true,
				},
				{
					desc: "many incrementals",
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						gittest.SkipWithSHA256(tb) // sha256 only works with manifest files

						_, expectedRepoPath := gittest.CreateRepository(tb, ctx, cfg)

						repo, _ := gittest.CreateRepository(tb, ctx, cfg)

						root := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
						)
						master1 := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
							gittest.WithParents(root),
						)
						other := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("other"),
							gittest.WithParents(root),
						)
						gittest.Exec(tb, cfg, "-C", expectedRepoPath, "symbolic-ref", "HEAD", "refs/heads/master")
						bundle1 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "bundle", "create", "-",
							"HEAD",
							"refs/heads/master",
							"refs/heads/other",
						)
						refs1 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "show-ref", "--head")

						master2 := gittest.WriteCommit(tb, cfg, expectedRepoPath,
							gittest.WithBranch("master"),
							gittest.WithParents(master1),
						)
						bundle2 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "bundle", "create", "-",
							"HEAD",
							"^"+master1.String(),
							"^"+other.String(),
							"refs/heads/master",
							"refs/heads/other",
						)
						refs2 := gittest.Exec(tb, cfg, "-C", expectedRepoPath, "show-ref", "--head")

						relativePath := stripRelativePath(tb, repo)
						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join(relativePath, "LATEST"):               backupID,
							filepath.Join(relativePath, backupID, "LATEST"):     "002",
							filepath.Join(relativePath, backupID, "001.bundle"): bundle1,
							filepath.Join(relativePath, backupID, "002.bundle"): bundle2,
							filepath.Join(relativePath, backupID, "001.refs"):   refs1,
							filepath.Join(relativePath, backupID, "002.refs"):   refs2,
						})

						checksum := new(git.Checksum)
						checksum.Add(git.NewReference("HEAD", master2))
						checksum.Add(git.NewReference("refs/heads/master", master2))
						checksum.Add(git.NewReference("refs/heads/other", other))

						return repo, checksum
					},
					expectExists: true,
				},
				{
					desc: "manifest, empty backup",
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(tb, ctx, cfg)

						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join("manifests", repo.GetStorageName(), repo.GetRelativePath(), backupID+".toml"): fmt.Sprintf(
								`object_format = %q
head_reference = 'refs/heads/banana'

[[steps]]
bundle_path = 'repo.bundle'
ref_path = 'repo.refs'
custom_hooks_path = 'custom_hooks.tar'
`, gittest.DefaultObjectHash.Format),
							"repo.refs": "",
						})

						return repo, new(git.Checksum)
					},
					expectExists:          true,
					expectedHeadReference: "refs/heads/banana",
				},
				{
					desc: "manifest",
					setup: func(tb testing.TB) (*gitalypb.Repository, *git.Checksum) {
						repo, _ := gittest.CreateRepository(tb, ctx, cfg)

						testhelper.WriteFiles(tb, backupRoot, map[string]any{
							filepath.Join("manifests", repo.GetStorageName(), repo.GetRelativePath(), backupID+".toml"): fmt.Sprintf(
								`object_format = %q
head_reference = 'refs/heads/banana'

[[steps]]
bundle_path = 'repo.bundle'
ref_path = 'repo.refs'
custom_hooks_path = 'custom_hooks.tar'
`, gittest.DefaultObjectHash.Format),
							"repo.bundle": repoBundle,
							"repo.refs":   repoRefs,
						})

						checksum := gittest.ChecksumRepo(tb, cfg, repoPath)
						// Negate off the default branch since the manifest is
						// explicitly setting a different unborn branch that
						// will not be part of the checksum.
						checksum.Add(git.NewReference("HEAD", commitID))

						return repo, checksum
					},
					expectExists:          true,
					expectedHeadReference: "refs/heads/banana",
				},
			} {
				t.Run(tc.desc, func(t *testing.T) {
					repo, expectedChecksum := tc.setup(t)

					sink := backup.NewFilesystemSink(backupRoot)
					defer testhelper.MustClose(t, sink)

					locator, err := backup.ResolveLocator("pointer", sink)
					require.NoError(t, err)

					fsBackup := managerTC.setup(t, sink, locator)
					err = fsBackup.Restore(ctx, &backup.RestoreRequest{
						Server:           storage.ServerInfo{Address: cfg.SocketPath, Token: cfg.Auth.Token},
						Repository:       repo,
						VanityRepository: repo,
						AlwaysCreate:     tc.alwaysCreate,
						BackupID:         backupID,
					})
					if tc.expectedErrAs != nil {
						require.ErrorAs(t, err, &tc.expectedErrAs)
					} else {
						require.NoError(t, err)
					}

					exists, err := repoClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
						Repository: repo,
					})
					require.NoError(t, err)
					require.Equal(t, tc.expectExists, exists.Exists, "repository exists")

					if expectedChecksum != nil {
						checksum, err := repoClient.CalculateChecksum(ctx, &gitalypb.CalculateChecksumRequest{
							Repository: repo,
						})
						require.NoError(t, err)

						require.Equal(t, expectedChecksum.String(), checksum.GetChecksum())
					}

					if len(tc.expectedPaths) > 0 {
						// Restore has to use the rewritten path as the relative path due to the test creating
						// the repository through Praefect. In order to get to the correct disk paths, we need
						// to get the replica path of the rewritten repository.
						repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))

						for _, p := range tc.expectedPaths {
							require.FileExists(t, filepath.Join(repoPath, p))
						}
					}

					if len(tc.expectedHeadReference) > 0 {
						repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))

						ref := gittest.GetSymbolicRef(t, cfg, repoPath, "HEAD")
						require.Equal(t, tc.expectedHeadReference, git.ReferenceName(ref.Target))
					}
				})
			}
		})
	}
}

func TestManager_CreateRestore_contextServerInfo(t *testing.T) {
	gittest.SkipWithSHA256(t)

	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())

	backupRoot := testhelper.TempDir(t)

	pool := client.NewPool()
	defer testhelper.MustClose(t, pool)

	sink := backup.NewFilesystemSink(backupRoot)
	defer testhelper.MustClose(t, sink)

	locator, err := backup.ResolveLocator("pointer", sink)
	require.NoError(t, err)

	fsBackup := backup.NewManager(sink, locator, pool)

	ctx = testhelper.MergeIncomingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	require.NoError(t, fsBackup.Create(ctx, &backup.CreateRequest{
		Repository: repo,
		BackupID:   "abc123",
	}))
	require.NoError(t, fsBackup.Restore(ctx, &backup.RestoreRequest{
		Repository: repo,
	}))
}

func TestResolveLocator(t *testing.T) {
	gittest.SkipWithSHA256(t)

	t.Parallel()

	for _, tc := range []struct {
		layout      string
		expectedErr string
	}{
		{layout: "legacy"},
		{layout: "pointer"},
		{
			layout:      "unknown",
			expectedErr: "unknown layout: \"unknown\"",
		},
	} {
		t.Run(tc.layout, func(t *testing.T) {
			l, err := backup.ResolveLocator(tc.layout, nil)

			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
				return
			}

			require.NotNil(t, l)
		})
	}
}

func joinBackupPath(tb testing.TB, backupRoot string, repo storage.Repository, elements ...string) string {
	return filepath.Join(append([]string{
		backupRoot,
		stripRelativePath(tb, repo),
	}, elements...)...)
}

func stripRelativePath(tb testing.TB, repo storage.Repository) string {
	return strings.TrimSuffix(repo.GetRelativePath(), ".git")
}

func mustCreateCustomHooksArchive(t *testing.T, ctx context.Context) string {
	t.Helper()

	tmpDir := testhelper.TempDir(t)

	hooksDirPath := filepath.Join(tmpDir, "custom_hooks")
	require.NoError(t, os.Mkdir(hooksDirPath, os.ModePerm))

	require.NoError(t, os.WriteFile(filepath.Join(hooksDirPath, "pre-commit.sample"), []byte("foo"), os.ModePerm))
	require.NoError(t, os.WriteFile(filepath.Join(hooksDirPath, "prepare-commit-msg.sample"), []byte("bar"), os.ModePerm))
	require.NoError(t, os.WriteFile(filepath.Join(hooksDirPath, "pre-push.sample"), []byte("baz"), os.ModePerm))

	archivePath := filepath.Join(tmpDir, "custom_hooks.tar")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	defer testhelper.MustClose(t, file)

	require.NoError(t, archive.WriteTarball(ctx, testhelper.SharedLogger(t), file, tmpDir, "custom_hooks"))

	return archivePath
}
