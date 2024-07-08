package quarantine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

// entry represents a filesystem entry. An entry represents a dir if children is a non-nil map.
type entry struct {
	children map[string]entry
	contents string
}

func (e entry) create(t *testing.T, root string) {
	// An entry cannot have both file contents and children.
	require.True(t, e.contents == "" || e.children == nil, "An entry cannot have both file contents and children")

	if e.children != nil {
		require.NoError(t, os.Mkdir(root, perm.PrivateDir))

		for name, child := range e.children {
			child.create(t, filepath.Join(root, name))
		}
	} else {
		require.NoError(t, os.WriteFile(root, []byte(e.contents), perm.PublicFile))
	}
}

func TestQuarantine_lifecycle(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)

	t.Run("quarantine directory gets created", func(t *testing.T) {
		quarantine, err := New(ctx, repo, logger, locator)
		require.NoError(t, err)

		relativeQuarantinePath, err := filepath.Rel(repoPath, quarantine.dir.Path())
		require.NoError(t, err)

		require.Equal(t, repo, quarantine.repo)
		testhelper.ProtoEqual(t, &gitalypb.Repository{
			StorageName:        repo.StorageName,
			RelativePath:       repo.RelativePath,
			GitObjectDirectory: relativeQuarantinePath,
			GitAlternateObjectDirectories: []string{
				"objects",
			},
			GlRepository:  repo.GlRepository,
			GlProjectPath: repo.GlProjectPath,
		}, quarantine.quarantinedRepo)
		require.Equal(t, locator, quarantine.locator)

		require.DirExists(t, quarantine.dir.Path())
	})

	t.Run("context cancellation cleans up quarantine directory", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctx)

		quarantine, err := New(ctx, repo, logger, locator)
		require.NoError(t, err)

		require.DirExists(t, quarantine.dir.Path())
		cancel()
		quarantine.dir.WaitForCleanup()
		require.NoDirExists(t, quarantine.dir.Path())
	})
}

func TestQuarantine_Migrate(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	locator := config.NewLocator(cfg)
	logger := testhelper.NewLogger(t)

	t.Run("no changes", func(t *testing.T) {
		ctx := testhelper.Context(t)

		repo, repoPath := gittest.CreateRepository(t, ctx, cfg,
			gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})

		oldContents := listEntries(t, repoPath)

		quarantine, err := New(ctx, repo, logger, locator)
		require.NoError(t, err)

		require.NoError(t, quarantine.Migrate(ctx))

		require.Equal(t, oldContents, listEntries(t, repoPath))
	})

	t.Run("simple change", func(t *testing.T) {
		ctx := testhelper.Context(t)

		repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		oldContents := listEntries(t, repoPath)
		require.NotContains(t, oldContents, "objects/file")

		quarantine, err := New(ctx, repo, logger, locator)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(quarantine.dir.Path(), "file"), []byte("foobar"), perm.PublicFile))
		require.NoError(t, quarantine.Migrate(ctx))

		newContents := listEntries(t, repoPath)
		require.Contains(t, newContents, "objects/file")

		oldContents["objects/file"] = "foobar"
		require.Equal(t, oldContents, newContents)
	})

	t.Run("simple change into existing quarantine", func(t *testing.T) {
		ctx := testhelper.Context(t)

		repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		repoContents := listEntries(t, repoPath)
		require.NotContains(t, repoContents, "objects/file")

		quarantine, err := New(ctx, repo, logger, locator)
		require.NoError(t, err)

		require.Empty(t, listEntries(t, quarantine.dir.Path()))

		// Quarantine the already quarantined repository and write the object there. We expect the
		// object to be migrated from the second level quarantine to the first level quarantine. The
		// main repository should stay untouched.
		recursiveQuarantine, err := New(ctx, quarantine.QuarantinedRepo(), logger, locator)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(recursiveQuarantine.dir.Path(), "file"), []byte("foobar"), perm.PublicFile))
		require.NoError(t, recursiveQuarantine.Migrate(ctx))

		// The main repo should be untouched and still not contain the object.
		require.Equal(t, repoContents, listEntries(t, repoPath))

		// The quarantine should contain the object.
		require.Equal(t,
			map[string]string{"file": "foobar"},
			listEntries(t, quarantine.dir.Path()),
		)

		// Recursive quarantine should no longer exist.
		require.NoDirExists(t, recursiveQuarantine.dir.Path())
	})
}

func TestApply(t *testing.T) {
	cfg := testcfg.Build(t)

	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	quarantineDir := t.TempDir()
	relPath, err := filepath.Rel(repoPath, quarantineDir)
	require.NoError(t, err)

	for _, tc := range []struct {
		desc                          string
		gitObjectDirectory            string
		gitAlternateObjectDirectories []string
		expectedRepo                  *gitalypb.Repository
	}{
		{
			desc:                          "default objects directory",
			gitAlternateObjectDirectories: []string{"alternate_directory"},
			expectedRepo: &gitalypb.Repository{
				StorageName:                   repo.StorageName,
				GitObjectDirectory:            relPath,
				GitAlternateObjectDirectories: []string{"objects", "alternate_directory"},
				RelativePath:                  repo.RelativePath,
				GlProjectPath:                 repo.GlProjectPath,
				GlRepository:                  repo.GlRepository,
			},
		},
		{
			desc: "default objects directory with alternates",
			expectedRepo: &gitalypb.Repository{
				StorageName:                   repo.StorageName,
				GitObjectDirectory:            relPath,
				GitAlternateObjectDirectories: []string{"objects"},
				RelativePath:                  repo.RelativePath,
				GlProjectPath:                 repo.GlProjectPath,
				GlRepository:                  repo.GlRepository,
			},
		},
		{
			desc:               "custom objects directory",
			gitObjectDirectory: "custom_directory",
			expectedRepo: &gitalypb.Repository{
				StorageName:                   repo.StorageName,
				GitObjectDirectory:            relPath,
				GitAlternateObjectDirectories: []string{"custom_directory"},
				RelativePath:                  repo.RelativePath,
				GlProjectPath:                 repo.GlProjectPath,
				GlRepository:                  repo.GlRepository,
			},
		},
		{
			desc:                          "custom objects directory with alternates",
			gitObjectDirectory:            "custom_directory",
			gitAlternateObjectDirectories: []string{"alternate_directory"},
			expectedRepo: &gitalypb.Repository{
				StorageName:                   repo.StorageName,
				GitObjectDirectory:            relPath,
				GitAlternateObjectDirectories: []string{"custom_directory", "alternate_directory"},
				RelativePath:                  repo.RelativePath,
				GlProjectPath:                 repo.GlProjectPath,
				GlRepository:                  repo.GlRepository,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			originalRepo := proto.Clone(repo).(*gitalypb.Repository)
			originalRepo.GitObjectDirectory = tc.gitObjectDirectory
			originalRepo.GitAlternateObjectDirectories = tc.gitAlternateObjectDirectories

			quarantinedRepo, err := Apply(repoPath, originalRepo, quarantineDir)
			require.NoError(t, err)

			testhelper.ProtoEqual(t, tc.expectedRepo, quarantinedRepo)
		})
	}
}

func TestMigrate(t *testing.T) {
	for _, tc := range []struct {
		desc     string
		source   entry
		target   entry
		expected map[string]string
	}{
		{
			desc: "simple migration",
			source: entry{children: map[string]entry{
				"a": {contents: "a"},
				"dir": {children: map[string]entry{
					"b": {contents: "b"},
					"c": {contents: "c"},
				}},
			}},
			target: entry{children: map[string]entry{}},
			expected: map[string]string{
				"a":     "a",
				"dir/":  "",
				"dir/b": "b",
				"dir/c": "c",
			},
		},
		{
			desc: "empty directories",
			source: entry{children: map[string]entry{
				"empty": {children: map[string]entry{
					"dir": {children: map[string]entry{
						"subdir": {children: map[string]entry{}},
					}},
				}},
			}},
			target: entry{children: map[string]entry{}},
			expected: map[string]string{
				"empty/":            "",
				"empty/dir/":        "",
				"empty/dir/subdir/": "",
			},
		},
		{
			desc: "conflicting migration",
			source: entry{children: map[string]entry{
				"does": {children: map[string]entry{
					"not": {children: map[string]entry{
						"exist": {children: map[string]entry{
							"a": {contents: "a"},
						}},
					}},
					"exist": {children: map[string]entry{
						"a": {contents: "a"},
					}},
				}},
			}},
			target: entry{children: map[string]entry{
				"does": {children: map[string]entry{
					"exist": {children: map[string]entry{
						"a": {contents: "conflicting contents"},
					}},
				}},
			}},
			expected: map[string]string{
				"does/":            "",
				"does/not/":        "",
				"does/not/exist/":  "",
				"does/not/exist/a": "a",
				"does/exist/":      "",
				"does/exist/a":     "conflicting contents",
			},
		},
		{
			desc: "dir/file conflict",
			source: entry{children: map[string]entry{
				"conflicting": {children: map[string]entry{
					"path": {children: map[string]entry{}},
				}},
			}},
			target: entry{children: map[string]entry{
				"conflicting": {children: map[string]entry{
					"path": {contents: "imafile"},
				}},
			}},
			expected: map[string]string{
				"conflicting/":     "",
				"conflicting/path": "imafile",
			},
		},
		{
			desc: "file/dir conflict",
			source: entry{children: map[string]entry{
				"conflicting": {children: map[string]entry{
					"path": {contents: "imafile"},
				}},
			}},
			target: entry{children: map[string]entry{
				"conflicting": {children: map[string]entry{
					"path": {children: map[string]entry{}},
				}},
			}},
			expected: map[string]string{
				"conflicting/":      "",
				"conflicting/path/": "",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			dir := testhelper.TempDir(t)

			source := filepath.Join(dir, "source")
			tc.source.create(t, source)

			target := filepath.Join(dir, "target")
			tc.target.create(t, target)

			require.NoError(t, migrate(source, target))
			require.Equal(t, tc.expected, listEntries(t, target))
			require.NoDirExists(t, source)
		})
	}
}

func listEntries(t *testing.T, root string) map[string]string {
	actual := make(map[string]string)

	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)

		if root == path {
			return nil
		}

		relativePath, err := filepath.Rel(root, path)
		require.NoError(t, err)

		if info.IsDir() {
			actual[relativePath+"/"] = ""
		} else {
			contents := testhelper.MustReadFile(t, path)
			actual[relativePath] = string(contents)
		}

		return nil
	}))

	return actual
}

func TestFinalizeObjectFile(t *testing.T) {
	t.Run("simple migration", func(t *testing.T) {
		dir := testhelper.TempDir(t)

		source := filepath.Join(dir, "a")
		target := filepath.Join(dir, "b")
		require.NoError(t, os.WriteFile(source, []byte("a"), perm.PublicFile))

		require.NoError(t, finalizeObjectFile(source, target))
		require.NoFileExists(t, source)
		require.Equal(t, []byte("a"), testhelper.MustReadFile(t, target))
	})

	t.Run("cross-directory migration", func(t *testing.T) {
		sourceDir := testhelper.TempDir(t)
		targetDir := testhelper.TempDir(t)

		source := filepath.Join(sourceDir, "a")
		target := filepath.Join(targetDir, "a")
		require.NoError(t, os.WriteFile(source, []byte("a"), perm.PublicFile))

		require.NoError(t, finalizeObjectFile(source, target))
		require.NoFileExists(t, source)
		require.Equal(t, []byte("a"), testhelper.MustReadFile(t, target))
	})

	t.Run("migration with conflict", func(t *testing.T) {
		dir := testhelper.TempDir(t)

		source := filepath.Join(dir, "a")
		require.NoError(t, os.WriteFile(source, []byte("a"), perm.PublicFile))

		target := filepath.Join(dir, "b")
		require.NoError(t, os.WriteFile(target, []byte("b"), perm.PublicFile))

		// We do not expect an error in case the target file exists: given that objects and
		// packs are content addressable, a file with the same name should have the same
		// contents.
		require.NoError(t, finalizeObjectFile(source, target))
		require.NoFileExists(t, source)
		require.Equal(t, []byte("b"), testhelper.MustReadFile(t, target))
	})

	t.Run("migration with missing source", func(t *testing.T) {
		dir := testhelper.TempDir(t)

		source := filepath.Join(dir, "a")
		target := filepath.Join(dir, "b")

		err := finalizeObjectFile(source, target)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.NoFileExists(t, source)
		require.NoFileExists(t, target)
	})
}

type mockDirEntry struct {
	os.DirEntry
	name string
}

func (e mockDirEntry) Name() string {
	return e.name
}

func TestSortEntries(t *testing.T) {
	for _, tc := range []struct {
		desc     string
		entries  []string
		expected []string
	}{
		{
			desc: "empty",
		},
		{
			desc: "single entry",
			entries: []string{
				"foo",
			},
			expected: []string{
				"foo",
			},
		},
		{
			desc: "multiple non-pack entries are stable",
			entries: []string{
				"foo",
				"bar",
				"qux",
			},
			expected: []string{
				"foo",
				"bar",
				"qux",
			},
		},
		{
			desc: "packfile and metadata sorting",
			entries: []string{
				"pack.keep",
				"2a",
				"pack.rev",
				"pack.pack",
				"pack-foo",
				"pack.idx",
			},
			expected: []string{
				"2a",
				"pack.keep",
				"pack.pack",
				"pack.rev",
				"pack.idx",
				"pack-foo",
			},
		},
		{
			desc: "multiple packfiles",
			entries: []string{
				"pack-1.pack",
				"pack-2.keep",
				"pack-1.keep",
				"pack-2.rev",
				"pack-2.pack",
				"pack-2.idx",
				"pack-3.keep",
				"pack-3.rev",
				"pack-1.idx",
				"pack-3.idx",
				"pack-3.pack",
				"pack-1.rev",
			},
			expected: []string{
				// While we sort by suffix, the relative order should stay the same.
				"pack-2.keep",
				"pack-1.keep",
				"pack-3.keep",
				"pack-1.pack",
				"pack-2.pack",
				"pack-3.pack",
				"pack-2.rev",
				"pack-3.rev",
				"pack-1.rev",
				"pack-2.idx",
				"pack-1.idx",
				"pack-3.idx",
			},
		},
		{
			desc: "mixed packfiles and loose objects",
			entries: []string{
				"pack/pack-1b73f6861d229dc95dc223f0f5ea813aee8737ab.pack",
				"07/7923d0295d21536d8be837e8318a1592b040fe",
				"info/packs",
				"pack/pack-1b73f6861d229dc95dc223f0f5ea813aee8737ab.idx",
				"32/43774a285780f59f20c42d739600d596d9b1de",
				"pack/pack-4ce00e8f93ce33128d4c9e2b14931c458ded8ec2.pack",
				"pack/pack-4ce00e8f93ce33128d4c9e2b14931c458ded8ec2.idx",
			},
			expected: []string{
				"07/7923d0295d21536d8be837e8318a1592b040fe",
				"info/packs",
				"32/43774a285780f59f20c42d739600d596d9b1de",
				"pack/pack-1b73f6861d229dc95dc223f0f5ea813aee8737ab.pack",
				"pack/pack-4ce00e8f93ce33128d4c9e2b14931c458ded8ec2.pack",
				"pack/pack-1b73f6861d229dc95dc223f0f5ea813aee8737ab.idx",
				"pack/pack-4ce00e8f93ce33128d4c9e2b14931c458ded8ec2.idx",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var actualEntries []os.DirEntry
			for _, entry := range tc.entries {
				actualEntries = append(actualEntries, mockDirEntry{name: entry})
			}

			var expectedEntries []os.DirEntry
			for _, entry := range tc.expected {
				expectedEntries = append(expectedEntries, mockDirEntry{name: entry})
			}

			sortEntries(actualEntries)
			require.Equal(t, expectedEntries, actualEntries)
		})
	}
}
