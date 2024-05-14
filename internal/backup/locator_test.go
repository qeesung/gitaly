package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestLegacyLocator(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           t.Name(),
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	l := LegacyLocator{}

	t.Run("Begin/Commit Full", func(t *testing.T) {
		t.Parallel()

		expected := &Backup{
			ID:           "", // legacy storage can only store a single backup.
			Repository:   repo,
			ObjectFormat: git.ObjectHashSHA1.Format,
			Steps: []Step{
				{
					BundlePath:      repo.RelativePath + ".bundle",
					RefPath:         repo.RelativePath + ".refs",
					CustomHooksPath: filepath.Join(repo.RelativePath, "custom_hooks.tar"),
				},
			},
		}

		full := l.BeginFull(ctx, repo, "abc123")
		assert.Equal(t, expected, full)

		require.NoError(t, l.Commit(ctx, full))
	})

	t.Run("FindLatest", func(t *testing.T) {
		t.Parallel()

		expected := &Backup{
			ID:           "", // legacy storage can only store a single backup.
			Repository:   repo,
			ObjectFormat: git.ObjectHashSHA1.Format,
			Steps: []Step{
				{
					BundlePath:      repo.RelativePath + ".bundle",
					RefPath:         repo.RelativePath + ".refs",
					CustomHooksPath: filepath.Join(repo.RelativePath, "custom_hooks.tar"),
				},
			},
		}

		full, err := l.FindLatest(ctx, repo)
		require.NoError(t, err)

		assert.Equal(t, expected, full)
	})
}

func TestPointerLocator(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           t.Name(),
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	t.Run("Begin/Commit full", func(t *testing.T) {
		t.Parallel()

		backupPath := testhelper.TempDir(t)
		sink, err := ResolveSink(ctx, backupPath)
		require.NoError(t, err)
		var l Locator = PointerLocator{
			Sink: sink,
		}

		const expectedIncrement = "001"
		expected := &Backup{
			ID:           backupID,
			Repository:   repo,
			ObjectFormat: git.ObjectHashSHA1.Format,
			Steps: []Step{
				{
					BundlePath:      filepath.Join(repo.RelativePath, backupID, expectedIncrement+".bundle"),
					RefPath:         filepath.Join(repo.RelativePath, backupID, expectedIncrement+".refs"),
					CustomHooksPath: filepath.Join(repo.RelativePath, backupID, expectedIncrement+".custom_hooks.tar"),
				},
			},
		}

		full := l.BeginFull(ctx, repo, backupID)
		assert.Equal(t, expected, full)

		require.NoError(t, l.Commit(ctx, full))

		backupPointer := testhelper.MustReadFile(t, filepath.Join(backupPath, repo.RelativePath, "LATEST"))
		require.Equal(t, backupID, string(backupPointer))

		incrementPointer := testhelper.MustReadFile(t, filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"))
		require.Equal(t, expectedIncrement, string(incrementPointer))
	})

	t.Run("Begin/Commit incremental", func(t *testing.T) {
		t.Parallel()

		const fallbackBackupID = "fallback123"

		for _, tc := range []struct {
			desc             string
			setup            func(tb testing.TB, ctx context.Context, backupPath string)
			expectedBackupID string
			expectedOffset   int
		}{
			{
				desc:             "no previous backup",
				expectedBackupID: fallbackBackupID,
			},
			{
				desc:             "with previous backup",
				expectedBackupID: "abc123",
				expectedOffset:   1,
				setup: func(tb testing.TB, ctx context.Context, backupPath string) {
					require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, "abc123"), perm.SharedDir))
					require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "LATEST"), []byte("abc123"), perm.SharedFile))
					require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "abc123", "LATEST"), []byte("001"), perm.SharedFile))
				},
			},
		} {
			tc := tc

			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()

				backupPath := testhelper.TempDir(t)
				sink, err := ResolveSink(ctx, backupPath)
				require.NoError(t, err)
				var l Locator = PointerLocator{Sink: sink}

				if tc.setup != nil {
					tc.setup(t, ctx, backupPath)
				}

				var expected *Backup
				for i := 1; i <= 3; i++ {
					var previousRefPath, expectedIncrement string
					expected = &Backup{
						ID:           fallbackBackupID,
						Repository:   repo,
						ObjectFormat: git.ObjectHashSHA1.Format,
					}
					for incrementID := 1; incrementID <= i+tc.expectedOffset; incrementID++ {
						expectedIncrement = fmt.Sprintf("%03d", incrementID)
						step := Step{
							BundlePath:      filepath.Join(repo.RelativePath, tc.expectedBackupID, expectedIncrement+".bundle"),
							RefPath:         filepath.Join(repo.RelativePath, tc.expectedBackupID, expectedIncrement+".refs"),
							PreviousRefPath: previousRefPath,
							CustomHooksPath: filepath.Join(repo.RelativePath, tc.expectedBackupID, expectedIncrement+".custom_hooks.tar"),
						}
						expected.Steps = append(expected.Steps, step)
						previousRefPath = step.RefPath
					}

					step, err := l.BeginIncremental(ctx, repo, fallbackBackupID)
					require.NoError(t, err)
					require.Equal(t, expected, step)

					require.NoError(t, l.Commit(ctx, step))

					backupPointer := testhelper.MustReadFile(t, filepath.Join(backupPath, repo.RelativePath, "LATEST"))
					require.Equal(t, tc.expectedBackupID, string(backupPointer))

					incrementPointer := testhelper.MustReadFile(t, filepath.Join(backupPath, repo.RelativePath, tc.expectedBackupID, "LATEST"))
					require.Equal(t, expectedIncrement, string(incrementPointer))
				}
			})
		}
	})

	t.Run("FindLatest", func(t *testing.T) {
		t.Parallel()

		t.Run("no fallback", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			_, err = l.FindLatest(ctx, repo)
			require.ErrorIs(t, err, ErrDoesntExist)

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, backupID), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "LATEST"), []byte(backupID), perm.SharedFile))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"), []byte("003"), perm.SharedFile))
			expected := &Backup{
				ID:           backupID,
				Repository:   repo,
				ObjectFormat: git.ObjectHashSHA1.Format,
				Steps: []Step{
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "001.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "001.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "001.custom_hooks.tar"),
					},
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "002.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "002.refs"),
						PreviousRefPath: filepath.Join(repo.RelativePath, backupID, "001.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "002.custom_hooks.tar"),
					},
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "003.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "003.refs"),
						PreviousRefPath: filepath.Join(repo.RelativePath, backupID, "002.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "003.custom_hooks.tar"),
					},
				},
			}

			full, err := l.FindLatest(ctx, repo)
			require.NoError(t, err)
			require.Equal(t, expected, full)
		})

		t.Run("fallback", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink:     sink,
				Fallback: LegacyLocator{},
			}

			expectedFallback := &Backup{
				ID:           "",
				Repository:   repo,
				ObjectFormat: git.ObjectHashSHA1.Format,
				Steps: []Step{
					{
						BundlePath:      repo.RelativePath + ".bundle",
						RefPath:         repo.RelativePath + ".refs",
						CustomHooksPath: filepath.Join(repo.RelativePath, "custom_hooks.tar"),
					},
				},
			}

			fallbackFull, err := l.FindLatest(ctx, repo)
			require.NoError(t, err)
			require.Equal(t, expectedFallback, fallbackFull)

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, backupID), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "LATEST"), []byte(backupID), perm.SharedFile))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"), []byte("001"), perm.SharedFile))
			expected := &Backup{
				ID:           backupID,
				Repository:   repo,
				ObjectFormat: git.ObjectHashSHA1.Format,
				Steps: []Step{
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "001.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "001.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "001.custom_hooks.tar"),
					},
				},
			}

			full, err := l.FindLatest(ctx, repo)
			require.NoError(t, err)
			require.Equal(t, expected, full)
		})

		t.Run("invalid backup LATEST", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			_, err = l.FindLatest(ctx, repo)
			require.ErrorIs(t, err, ErrDoesntExist)

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "LATEST"), []byte("invalid"), perm.SharedFile))
			_, err = l.FindLatest(ctx, repo)
			require.EqualError(t, err, "pointer locator: find latest: find: find latest ID: storage service sink: new reader for \"TestPointerLocator/invalid/LATEST\": doesn't exist")
		})

		t.Run("invalid incremental LATEST", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			_, err = l.FindLatest(ctx, repo)
			require.ErrorIs(t, err, ErrDoesntExist)

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, backupID), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, "LATEST"), []byte(backupID), perm.SharedFile))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"), []byte("invalid"), perm.SharedFile))

			_, err = l.FindLatest(ctx, repo)
			require.EqualError(t, err, "pointer locator: find latest: find: determine increment ID: strconv.Atoi: parsing \"invalid\": invalid syntax")
		})
	})

	t.Run("Find", func(t *testing.T) {
		t.Parallel()

		t.Run("not found", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			_, err = l.Find(ctx, repo, backupID)
			require.ErrorIs(t, err, ErrDoesntExist)
		})

		t.Run("found", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, backupID), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"), []byte("003"), perm.SharedFile))
			expected := &Backup{
				ID:           backupID,
				Repository:   repo,
				ObjectFormat: git.ObjectHashSHA1.Format,
				Steps: []Step{
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "001.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "001.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "001.custom_hooks.tar"),
					},
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "002.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "002.refs"),
						PreviousRefPath: filepath.Join(repo.RelativePath, backupID, "001.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "002.custom_hooks.tar"),
					},
					{
						BundlePath:      filepath.Join(repo.RelativePath, backupID, "003.bundle"),
						RefPath:         filepath.Join(repo.RelativePath, backupID, "003.refs"),
						PreviousRefPath: filepath.Join(repo.RelativePath, backupID, "002.refs"),
						CustomHooksPath: filepath.Join(repo.RelativePath, backupID, "003.custom_hooks.tar"),
					},
				},
			}

			full, err := l.Find(ctx, repo, backupID)
			require.NoError(t, err)
			require.Equal(t, expected, full)
		})

		t.Run("invalid incremental LATEST", func(t *testing.T) {
			t.Parallel()

			backupPath := testhelper.TempDir(t)
			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}

			_, err = l.Find(ctx, repo, backupID)
			require.ErrorIs(t, err, ErrDoesntExist)

			require.NoError(t, os.MkdirAll(filepath.Join(backupPath, repo.RelativePath, backupID), perm.SharedDir))
			require.NoError(t, os.WriteFile(filepath.Join(backupPath, repo.RelativePath, backupID, "LATEST"), []byte("invalid"), perm.SharedFile))

			_, err = l.Find(ctx, repo, backupID)
			require.EqualError(t, err, "pointer locator: find: determine increment ID: strconv.Atoi: parsing \"invalid\": invalid syntax")
		})
	})
}

func TestManifestLocator_withFallback(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           t.Name(),
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	t.Run("BeginFull/Commit", func(t *testing.T) {
		t.Parallel()

		backupPath := testhelper.TempDir(t)
		sink, err := ResolveSink(ctx, backupPath)
		require.NoError(t, err)
		var l Locator = PointerLocator{
			Sink: sink,
		}
		l = NewManifestLocator(sink, l)

		full := l.BeginFull(ctx, repo, backupID)
		require.NoError(t, l.Commit(ctx, full))

		manifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, backupID+".toml"))
		require.Equal(t, fmt.Sprintf(`object_format = 'sha1'

[[steps]]
bundle_path = '%[1]s/%[2]s/001.bundle'
ref_path = '%[1]s/%[2]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/001.custom_hooks.tar'
`, repo.RelativePath, backupID), string(manifest))
	})

	t.Run("BeginIncremental/Commit", func(t *testing.T) {
		t.Parallel()

		backupPath := testhelper.TempDir(t)

		testhelper.WriteFiles(t, backupPath, map[string]any{
			filepath.Join(repo.RelativePath, "LATEST"):           "abc123",
			filepath.Join(repo.RelativePath, "abc123", "LATEST"): "001",
		})

		sink, err := ResolveSink(ctx, backupPath)
		require.NoError(t, err)
		var l Locator = PointerLocator{
			Sink: sink,
		}
		l = NewManifestLocator(sink, l)

		incremental, err := l.BeginIncremental(ctx, repo, backupID)
		require.NoError(t, err)
		require.NoError(t, l.Commit(ctx, incremental))

		manifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, backupID+".toml"))
		latestManifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, "+latest.toml"))

		expectedManifest := fmt.Sprintf(`object_format = 'sha1'

[[steps]]
bundle_path = '%[1]s/%[2]s/001.bundle'
ref_path = '%[1]s/%[2]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/001.custom_hooks.tar'

[[steps]]
bundle_path = '%[1]s/%[2]s/002.bundle'
ref_path = '%[1]s/%[2]s/002.refs'
previous_ref_path = '%[1]s/%[2]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/002.custom_hooks.tar'
`, repo.RelativePath, backupID)

		require.Equal(t, expectedManifest, string(manifest))
		require.Equal(t, expectedManifest, string(latestManifest))
	})
}

func TestManifestLocator(t *testing.T) {
	t.Parallel()

	const backupID = "abc123"

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           t.Name(),
	})
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

	t.Run("BeginFull/Commit", func(t *testing.T) {
		t.Parallel()

		backupPath := testhelper.TempDir(t)
		sink, err := ResolveSink(ctx, backupPath)
		require.NoError(t, err)
		var l Locator = NewManifestLocator(sink, nil)

		full := l.BeginFull(ctx, repo, backupID)
		require.NoError(t, l.Commit(ctx, full))

		manifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, backupID+".toml"))
		require.Equal(t, fmt.Sprintf(`object_format = ''

[[steps]]
bundle_path = '%[1]s/%[2]s/%[3]s/001.bundle'
ref_path = '%[1]s/%[2]s/%[3]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/%[3]s/001.custom_hooks.tar'
`, repo.StorageName, repo.RelativePath, backupID), string(manifest))
	})

	t.Run("BeginIncremental/Commit", func(t *testing.T) {
		t.Parallel()

		backupPath := testhelper.TempDir(t)

		testhelper.WriteFiles(t, backupPath, map[string]any{
			filepath.Join("manifests", repo.StorageName, repo.RelativePath, "+latest.toml"): fmt.Sprintf(`
object_format = 'sha1'

[[steps]]
bundle_path = '%[1]s/%[2]s/%[3]s/001.bundle'
ref_path = '%[1]s/%[2]s/%[3]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/%[3]s/001.custom_hooks.tar'
`, repo.StorageName, repo.RelativePath, backupID),
		})

		sink, err := ResolveSink(ctx, backupPath)
		require.NoError(t, err)
		var l Locator = NewManifestLocator(sink, nil)

		incremental, err := l.BeginIncremental(ctx, repo, backupID)
		require.NoError(t, err)
		require.NoError(t, l.Commit(ctx, incremental))

		manifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, backupID+".toml"))
		latestManifest := testhelper.MustReadFile(t, filepath.Join(backupPath, "manifests", repo.StorageName, repo.RelativePath, "+latest.toml"))

		expectedManifest := fmt.Sprintf(`object_format = 'sha1'

[[steps]]
bundle_path = '%[1]s/%[2]s/%[3]s/001.bundle'
ref_path = '%[1]s/%[2]s/%[3]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/%[3]s/001.custom_hooks.tar'

[[steps]]
bundle_path = '%[1]s/%[2]s/%[3]s/002.bundle'
ref_path = '%[1]s/%[2]s/%[3]s/002.refs'
previous_ref_path = '%[1]s/%[2]s/%[3]s/001.refs'
custom_hooks_path = '%[1]s/%[2]s/%[3]s/002.custom_hooks.tar'
`, repo.StorageName, repo.RelativePath, backupID)

		require.Equal(t, expectedManifest, string(manifest))
		require.Equal(t, expectedManifest, string(latestManifest))
	})
}

func TestManifestLocator_Find(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc           string
		repo           storage.Repository
		backupID       string
		setup          func(t *testing.T, ctx context.Context, backupPath string)
		expectedBackup *Backup
	}{
		{
			desc: "finds manifest",
			repo: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "vanity/repo.git",
			},
			backupID: "abc123",
			setup: func(t *testing.T, ctx context.Context, backupPath string) {
				testhelper.WriteFiles(t, backupPath, map[string]any{
					"vanity/repo/LATEST":        "abc123",
					"vanity/repo/abc123/LATEST": "002",
					"manifests/default/vanity/repo.git/abc123.toml": `object_format = 'sha1'

[[steps]]
bundle_path = 'path/to/001.bundle'
ref_path = 'path/to/001.refs'
custom_hooks_path = 'path/to/001.custom_hooks.tar'

[[steps]]
bundle_path = 'path/to/002.bundle'
ref_path = 'path/to/002.refs'
previous_ref_path = 'path/to/001.refs'
custom_hooks_path = 'path/to/002.custom_hooks.tar'
`,
				})
			},
			expectedBackup: &Backup{
				ID: "abc123",
				Repository: &gitalypb.Repository{
					StorageName:  "default",
					RelativePath: "vanity/repo.git",
				},
				ObjectFormat: "sha1",
				Steps: []Step{
					{
						BundlePath:      "path/to/001.bundle",
						RefPath:         "path/to/001.refs",
						CustomHooksPath: "path/to/001.custom_hooks.tar",
					},
					{
						BundlePath:      "path/to/002.bundle",
						RefPath:         "path/to/002.refs",
						PreviousRefPath: "path/to/001.refs",
						CustomHooksPath: "path/to/002.custom_hooks.tar",
					},
				},
			},
		},
		{
			desc: "fallback",
			repo: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "vanity/repo.git",
			},
			backupID: "abc123",
			setup: func(t *testing.T, ctx context.Context, backupPath string) {
				testhelper.WriteFiles(t, backupPath, map[string]any{
					"vanity/repo/LATEST":        "abc123",
					"vanity/repo/abc123/LATEST": "002",
				})
			},
			expectedBackup: &Backup{
				ID: "abc123",
				Repository: &gitalypb.Repository{
					StorageName:  "default",
					RelativePath: "vanity/repo.git",
				},
				ObjectFormat: "sha1",
				Steps: []Step{
					{
						BundlePath:      "vanity/repo/abc123/001.bundle",
						RefPath:         "vanity/repo/abc123/001.refs",
						CustomHooksPath: "vanity/repo/abc123/001.custom_hooks.tar",
					},
					{
						BundlePath:      "vanity/repo/abc123/002.bundle",
						RefPath:         "vanity/repo/abc123/002.refs",
						PreviousRefPath: "vanity/repo/abc123/001.refs",
						CustomHooksPath: "vanity/repo/abc123/002.custom_hooks.tar",
					},
				},
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			backupPath := testhelper.TempDir(t)

			tc.setup(t, ctx, backupPath)

			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}
			l = NewManifestLocator(sink, l)

			backup, err := l.Find(ctx, tc.repo, tc.backupID)
			require.NoError(t, err)

			require.Equal(t, tc.expectedBackup, backup)
		})
	}
}

func TestManifestLocator_FindLatest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc           string
		repo           storage.Repository
		setup          func(t *testing.T, ctx context.Context, backupPath string)
		expectedBackup *Backup
	}{
		{
			desc: "finds manifest",
			repo: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "vanity/repo.git",
			},
			setup: func(t *testing.T, ctx context.Context, backupPath string) {
				testhelper.WriteFiles(t, backupPath, map[string]any{
					"vanity/repo/LATEST":        "abc123",
					"vanity/repo/abc123/LATEST": "002",
					"manifests/default/vanity/repo.git/+latest.toml": `object_format = 'sha1'

[[steps]]
bundle_path = 'manifest-path/to/001.bundle'
ref_path = 'manifest-path/to/001.refs'
custom_hooks_path = 'manifest-path/to/001.custom_hooks.tar'

[[steps]]
bundle_path = 'manifest-path/to/002.bundle'
ref_path = 'manifest-path/to/002.refs'
previous_ref_path = 'manifest-path/to/001.refs'
custom_hooks_path = 'manifest-path/to/002.custom_hooks.tar'
`,
				})
			},
			expectedBackup: &Backup{
				ID: "+latest",
				Repository: &gitalypb.Repository{
					StorageName:  "default",
					RelativePath: "vanity/repo.git",
				},
				ObjectFormat: "sha1",
				Steps: []Step{
					{
						BundlePath:      "manifest-path/to/001.bundle",
						RefPath:         "manifest-path/to/001.refs",
						CustomHooksPath: "manifest-path/to/001.custom_hooks.tar",
					},
					{
						BundlePath:      "manifest-path/to/002.bundle",
						RefPath:         "manifest-path/to/002.refs",
						PreviousRefPath: "manifest-path/to/001.refs",
						CustomHooksPath: "manifest-path/to/002.custom_hooks.tar",
					},
				},
			},
		},
		{
			desc: "fallback",
			repo: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "vanity/repo.git",
			},
			setup: func(t *testing.T, ctx context.Context, backupPath string) {
				testhelper.WriteFiles(t, backupPath, map[string]any{
					"vanity/repo/LATEST":        "abc123",
					"vanity/repo/abc123/LATEST": "002",
				})
			},
			expectedBackup: &Backup{
				ID: "abc123",
				Repository: &gitalypb.Repository{
					StorageName:  "default",
					RelativePath: "vanity/repo.git",
				},
				ObjectFormat: "sha1",
				Steps: []Step{
					{
						BundlePath:      "vanity/repo/abc123/001.bundle",
						RefPath:         "vanity/repo/abc123/001.refs",
						CustomHooksPath: "vanity/repo/abc123/001.custom_hooks.tar",
					},
					{
						BundlePath:      "vanity/repo/abc123/002.bundle",
						RefPath:         "vanity/repo/abc123/002.refs",
						PreviousRefPath: "vanity/repo/abc123/001.refs",
						CustomHooksPath: "vanity/repo/abc123/002.custom_hooks.tar",
					},
				},
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			backupPath := testhelper.TempDir(t)

			tc.setup(t, ctx, backupPath)

			sink, err := ResolveSink(ctx, backupPath)
			require.NoError(t, err)
			var l Locator = PointerLocator{
				Sink: sink,
			}
			l = NewManifestLocator(sink, l)

			backup, err := l.FindLatest(ctx, tc.repo)
			require.NoError(t, err)

			require.Equal(t, tc.expectedBackup, backup)
		})
	}
}
