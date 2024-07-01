package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRepoCreateBundle(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc           string
		opts           *CreateBundleOpts
		setup          func(t *testing.T, cfg config.Cfg, repo *Repo, repoPath string)
		expectedErr    error
		expectComplete bool
		expectedRefs   []git.ReferenceName
	}{
		{
			desc:        "empty bundle",
			setup:       func(t *testing.T, cfg config.Cfg, repo *Repo, repoPath string) {},
			expectedErr: fmt.Errorf("create bundle: %w", ErrEmptyBundle),
		},
		{
			desc: "complete",
			setup: func(t *testing.T, cfg config.Cfg, repo *Repo, repoPath string) {
				masterID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(masterID), gittest.WithBranch("feature"))
			},
			expectComplete: true,
			expectedRefs:   []git.ReferenceName{"refs/heads/feature", git.DefaultRef, "HEAD"},
		},
		{
			desc: "complete patterns",
			setup: func(t *testing.T, cfg config.Cfg, repo *Repo, repoPath string) {
				masterID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(masterID), gittest.WithBranch("feature"))
			},
			opts: &CreateBundleOpts{
				Patterns: strings.NewReader("refs/heads/feature\nrefs/heads/ignored_missing_ref\n" + git.DefaultRef.String()),
			},
			expectComplete: true,
			expectedRefs:   []git.ReferenceName{"refs/heads/feature", git.DefaultRef},
		},
		{
			desc: "partial patterns",
			setup: func(t *testing.T, cfg config.Cfg, repo *Repo, repoPath string) {
				masterID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(masterID), gittest.WithBranch("feature"))
			},
			opts: &CreateBundleOpts{
				Patterns: strings.NewReader("refs/heads/feature"),
			},
			expectedRefs: []git.ReferenceName{"refs/heads/feature"},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)

			cfg, repo, repoPath := setupRepo(t)
			tc.setup(t, cfg, repo, repoPath)

			bundle, err := os.Create(filepath.Join(testhelper.TempDir(t), "bundle"))
			require.NoError(t, err)

			err = repo.CreateBundle(ctx, bundle, tc.opts)
			if tc.expectedErr == nil {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.expectedErr, err)
				return
			}

			require.NoError(t, bundle.Close())

			if tc.expectComplete {
				output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "verify", bundle.Name())
				require.Contains(t, string(output), "The bundle records a complete history")
			}

			var refNames []git.ReferenceName
			output := gittest.Exec(t, cfg, "-C", repoPath, "bundle", "list-heads", bundle.Name())
			decoder := git.NewShowRefDecoder(bytes.NewReader(output))
			for {
				var ref git.Reference
				err := decoder.Decode(&ref)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)

				refNames = append(refNames, ref.Name)
			}

			require.Equal(t, tc.expectedRefs, refNames)
		})
	}
}

func TestRepo_CloneBundle(t *testing.T) {
	t.Parallel()

	createBundle := func(tb testing.TB, cfg config.Cfg, repoPath string) io.Reader {
		tmp := testhelper.TempDir(tb)
		bundlePath := filepath.Join(tmp, "test.bundle")
		gittest.BundleRepo(t, cfg, repoPath, bundlePath)

		bundle, err := os.Open(bundlePath)
		require.NoError(t, err)
		t.Cleanup(func() { testhelper.MustClose(t, bundle) })

		return bundle
	}

	type setupData struct {
		reader          io.Reader
		expectedHeadRef git.ReferenceName
		expectedRefs    []git.Reference
		expectedErr     error
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData
	}{
		{
			desc: "clone from Git bundle",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData {
				// Create a repository used to generate a Git bundle from.
				_, bundleRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				// Write objects to the repository so the contents of the extracted bundle can be
				// validated. Also change the repository HEAD to validate that the cloned repository
				// HEAD is also updated.
				mainOID := gittest.WriteCommit(t, cfg, bundleRepoPath, gittest.WithBranch(git.DefaultBranch))
				featureOID := gittest.WriteCommit(t, cfg, bundleRepoPath, gittest.WithBranch("feature"), gittest.WithParents(mainOID))
				gittest.Exec(t, cfg, "-C", bundleRepoPath, "symbolic-ref", "HEAD", "refs/heads/feature")

				return setupData{
					reader:          createBundle(t, cfg, bundleRepoPath),
					expectedHeadRef: git.NewReferenceNameFromBranchName("feature"),
					expectedRefs: []git.Reference{
						git.NewReference(git.NewReferenceNameFromBranchName("feature"), featureOID),
						git.NewReference(git.DefaultRef, mainOID),
					},
				}
			},
		},
		{
			desc: "empty bundle",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData {
				// If the Git bundle is empty, the repository will fail to be cloned.
				return setupData{
					reader:      new(bytes.Reader),
					expectedErr: structerr.New("waiting for git-clone: exit status 128"),
				}
			},
		},
		{
			desc: "invalid bundle",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData {
				// If the Git bundle is invalid, the repository will fail to be cloned.
				return setupData{
					reader:      strings.NewReader("invalid bundle"),
					expectedErr: structerr.New("waiting for git-clone: exit status 128"),
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)
			gitCmdFactory := gittest.NewCommandFactory(t, cfg)
			catfileCache := catfile.NewCache(cfg)
			defer catfileCache.Stop()

			repoProto := &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: gittest.NewRepositoryName(t),
			}

			data := tc.setup(t, ctx, cfg)
			repo := New(testhelper.NewLogger(t), config.NewLocator(cfg), gitCmdFactory, catfileCache, repoProto)

			err := repo.CloneBundle(ctx, data.reader)
			if data.expectedErr != nil {
				require.ErrorContains(t, err, data.expectedErr.Error())
				return
			}
			require.NoError(t, err)

			repoPath, err := repo.Path(ctx)
			require.NoError(t, err)

			// Verify connectivity and validity of the repository objects.
			gittest.Exec(t, cfg, "-C", repoPath, "fsck")

			// Verify HEAD has been set correctly.
			headRef, err := repo.HeadReference(ctx)
			require.NoError(t, err)
			require.Equal(t, data.expectedHeadRef, headRef)

			// Verify the repository has the correct references.
			refs, err := repo.GetReferences(ctx)
			require.NoError(t, err)
			require.Equal(t, data.expectedRefs, refs)

			// Verify the repository does not have remote set.
			remotes := gittest.Exec(t, cfg, "-C", repoPath, "remote")
			require.Empty(t, remotes)
		})
	}
}

func TestRepo_FetchBundle(t *testing.T) {
	t.Parallel()

	createBundle := func(tb testing.TB, cfg config.Cfg, repoPath string) io.Reader {
		tmp := testhelper.TempDir(tb)
		bundlePath := filepath.Join(tmp, "test.bundle")
		gittest.BundleRepo(t, cfg, repoPath, bundlePath)

		bundle, err := os.Open(bundlePath)
		require.NoError(t, err)
		t.Cleanup(func() { testhelper.MustClose(t, bundle) })

		return bundle
	}

	type testSetup struct {
		reader          io.Reader
		expectedHeadRef git.ReferenceName
		expectedRefs    []git.Reference
	}

	for _, tc := range []struct {
		desc                string
		opts                *CreateBundleOpts
		setup               func(t *testing.T, ctx context.Context, cfg config.Cfg, targetRepoPath string) testSetup
		updateHead          bool
		expectedErrAs       any
		expectedErrContains string
	}{
		{
			desc: "no HEAD update",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg, targetRepoPath string) testSetup {
				_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, targetRepoPath, gittest.WithBranch(git.DefaultBranch))

				mainOid := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch))
				featureOid := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("feature"), gittest.WithParents(mainOid))
				gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD", "refs/heads/feature")

				return testSetup{
					reader:          createBundle(t, cfg, sourceRepoPath),
					expectedHeadRef: git.DefaultRef,
					expectedRefs: []git.Reference{
						git.NewReference(git.NewReferenceNameFromBranchName("feature"), featureOid),
						git.NewReference(git.DefaultRef, mainOid),
					},
				}
			},
		},
		{
			desc:       "HEAD update",
			updateHead: true,
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg, targetRepoPath string) testSetup {
				_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})

				gittest.WriteCommit(t, cfg, targetRepoPath, gittest.WithBranch(git.DefaultBranch))

				mainOid := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch))
				featureOid := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch("feature"), gittest.WithParents(mainOid))
				gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD", "refs/heads/feature")

				return testSetup{
					reader:          createBundle(t, cfg, sourceRepoPath),
					expectedHeadRef: git.NewReferenceNameFromBranchName("feature"),
					expectedRefs: []git.Reference{
						git.NewReference(git.NewReferenceNameFromBranchName("feature"), featureOid),
						git.NewReference(git.DefaultRef, mainOid),
					},
				}
			},
		},
		{
			desc: "empty bundle",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg, targetRepoPath string) testSetup {
				return testSetup{reader: new(bytes.Reader)}
			},
			expectedErrAs:       FetchFailedError{},
			expectedErrContains: "fetch bundle: exit status 128",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			txManager := transaction.NewTrackingManager()
			cfg, repo, repoPath := setupRepo(t)
			data := tc.setup(t, ctx, cfg, repoPath)

			err := repo.FetchBundle(ctx, txManager, data.reader, &FetchBundleOpts{
				UpdateHead: tc.updateHead,
			})
			if tc.expectedErrAs != nil {
				require.ErrorAs(t, err, &tc.expectedErrAs)
			}
			if tc.expectedErrContains != "" {
				require.ErrorContains(t, err, tc.expectedErrContains)
				return
			}
			require.NoError(t, err)

			headRef, err := repo.HeadReference(ctx)
			require.NoError(t, err)
			require.Equal(t, data.expectedHeadRef, headRef)

			refs, err := repo.GetReferences(ctx)
			require.NoError(t, err)
			require.Equal(t, data.expectedRefs, refs)
		})
	}
}
