package repository

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	gitalyx509 "gitlab.com/gitlab-org/gitaly/v16/internal/x509"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestCreateFork_successful(t *testing.T) {
	// We need to inject this once across all tests given that crypto/x509 only initializes
	// certificates once. Changing injected certs during our tests is thus not going to fly well
	// and would cause failure. We should eventually address this and provide better testing
	// utilities around this, but now's not the time.
	certificate := testhelper.GenerateCertificate(t)
	t.Setenv(gitalyx509.SSLCertFile, certificate.CertPath)

	for _, tt := range []struct {
		name   string
		secure bool
	}{
		{
			name:   "secure",
			secure: true,
		},
		{
			name: "insecure",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testcfg.Build(t)

			testcfg.BuildGitalyHooks(t, cfg)
			testcfg.BuildGitalySSH(t, cfg)

			var client gitalypb.RepositoryServiceClient
			if tt.secure {
				cfg.TLS = config.TLS{
					CertPath: certificate.CertPath,
					KeyPath:  certificate.KeyPath,
				}
				cfg.TLSListenAddr = "localhost:0"

				client, cfg.TLSListenAddr = runRepositoryService(t, cfg)
			} else {
				client, cfg.SocketPath = runRepositoryService(t, cfg)
			}

			ctx := testhelper.Context(t)
			ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

			repo, _ := gittest.CreateRepository(t, ctx, cfg)

			forkedRepo := &gitalypb.Repository{
				RelativePath: gittest.NewRepositoryName(t),
				StorageName:  repo.GetStorageName(),
			}

			_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
				Repository:       forkedRepo,
				SourceRepository: repo,
			})
			require.NoError(t, err)

			replicaPath := gittest.GetReplicaPath(t, ctx, cfg, forkedRepo)
			forkedRepoPath := filepath.Join(cfg.Storages[0].Path, replicaPath)

			gittest.Exec(t, cfg, "-C", forkedRepoPath, "fsck")
			require.Empty(t, gittest.Exec(t, cfg, "-C", forkedRepoPath, "remote"))

			_, err = os.Lstat(filepath.Join(forkedRepoPath, "hooks"))
			require.True(t, os.IsNotExist(err), "hooks directory should not have been created")
		})
	}
}

func TestCreateFork_revision(t *testing.T) {
	t.Parallel()

	mainRef := git.ReferenceName("refs/heads/main")
	wipRef := git.ReferenceName("refs/heads/wip")
	unknownRef := []byte("refs/heads/unknown")
	invalidRef := []byte("v1.2.3")
	ambiguousRef := git.ReferenceName("refs/heads/v1.0.0")

	cfg, client := setupRepositoryService(t)

	ctx := testhelper.Context(t)
	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	for _, tt := range []struct {
		name         string
		revision     []byte
		expectedRefs []git.ReferenceName
		expectedErr  string
	}{
		{
			name:         "all branches",
			revision:     nil,
			expectedRefs: []git.ReferenceName{mainRef, wipRef, "refs/tags/v1.0.0"},
		},
		{
			name:         "single revision with success",
			revision:     []byte(wipRef),
			expectedRefs: []git.ReferenceName{wipRef},
		},
		{
			name:        "single unknown revision with failure",
			revision:    unknownRef,
			expectedErr: "Remote branch unknown not found in upstream origin",
		},
		{
			name:        "single invalid revision with failure",
			revision:    invalidRef,
			expectedErr: "creating fork: reference is not a branch",
		},
		{
			name:        "ambiguous reference",
			revision:    []byte(ambiguousRef),
			expectedErr: "checking whether HEAD reference is sane: exit status 128",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

			commitID := gittest.WriteCommit(t, cfg, repoPath)
			for _, ref := range []git.ReferenceName{
				mainRef,
				wipRef,
				"refs/tags/v1.0.0",
			} {
				gittest.WriteRef(t, cfg, repoPath, ref, commitID)
			}

			forkedRepo := &gitalypb.Repository{
				RelativePath: gittest.NewRepositoryName(t),
				StorageName:  repo.GetStorageName(),
			}

			_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
				Repository:       forkedRepo,
				SourceRepository: repo,
				Revision:         tt.revision,
			})

			if tt.expectedErr != "" {
				require.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)

				replicaPath := gittest.GetReplicaPath(t, ctx, cfg, forkedRepo)
				forkedRepoPath := filepath.Join(cfg.Storages[0].Path, replicaPath)

				actualRefs := strings.Split(strings.Trim(string(gittest.Exec(t, cfg, "-C", forkedRepoPath, "show-ref")), "\n"), "\n")
				var expectedRefs []string

				for _, ref := range tt.expectedRefs {
					expectedRefs = append(expectedRefs, fmt.Sprintf("%s %s", commitID.String(), ref))
				}

				require.Equal(t, expectedRefs, actualRefs)
			}
		})
	}
}

func TestCreateFork_refs(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryService(t)

	sourceRepo, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)

	// Prepare the source repository with a bunch of refs and a non-default HEAD ref so we can
	// assert that the target repo gets created with the correct set of refs.
	commitID := gittest.WriteCommit(t, cfg, sourceRepoPath)
	for _, ref := range []string{
		"refs/environments/something",
		"refs/heads/something",
		"refs/remotes/origin/something",
		"refs/tags/something",
	} {
		gittest.Exec(t, cfg, "-C", sourceRepoPath, "update-ref", ref, commitID.String())
	}
	gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD", "refs/heads/something")

	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	targetRepo := &gitalypb.Repository{
		RelativePath: gittest.NewRepositoryName(t),
		StorageName:  sourceRepo.GetStorageName(),
	}

	_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
		Repository:       targetRepo,
		SourceRepository: sourceRepo,
	})
	require.NoError(t, err)

	storagePath, err := config.NewLocator(cfg).GetStorageByName(ctx, targetRepo.GetStorageName())
	require.NoError(t, err)

	targetRepoPath := filepath.Join(storagePath, gittest.GetReplicaPath(t, ctx, cfg, targetRepo))

	require.Equal(t,
		[]string{
			commitID.String() + " refs/heads/something",
			commitID.String() + " refs/tags/something",
		},
		strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", targetRepoPath, "show-ref")), "\n"),
	)

	require.Equal(t,
		string(gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD")),
		string(gittest.Exec(t, cfg, "-C", targetRepoPath, "symbolic-ref", "HEAD")),
	)
}

func TestCreateFork_fsck(t *testing.T) {
	t.Parallel()

	cfg, client := setupRepositoryService(t)

	ctx := testhelper.Context(t)
	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	// Write a tree into the repository that's known-broken.
	treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Content: "content", Path: "dup", Mode: "100644"},
		{Content: "content", Path: "dup", Mode: "100644"},
	})

	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(),
		gittest.WithBranch("main"),
		gittest.WithTree(treeID),
	)

	forkedRepo := &gitalypb.Repository{
		RelativePath: gittest.NewRepositoryName(t),
		StorageName:  repo.GetStorageName(),
	}

	// Create a fork from the repository with the broken tree. This should work alright: repos
	// with preexisting broken objects that we already have on our disk anyway should not be
	// subject to additional consistency checks. Otherwise we might end up in a situation where
	// we retroactively tighten consistency checks for repositories such that preexisting repos
	// wouldn't be forkable anymore.
	_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
		Repository:       forkedRepo,
		SourceRepository: repo,
	})
	require.NoError(t, err)

	forkedRepoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, forkedRepo))

	// Verify that the broken tree is indeed in the fork and that it is reported as broken by
	// git-fsck(1).
	var stderr bytes.Buffer
	fsckCmd := gittest.NewCommand(t, cfg, "-C", forkedRepoPath, "fsck")
	fsckCmd.Stderr = &stderr

	require.EqualError(t, fsckCmd.Run(), "exit status 4")
	require.Equal(t, fmt.Sprintf("error in tree %s: duplicateEntries: contains duplicate file entries\n", treeID), stderr.String())
}

func TestCreateFork_targetExists(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc        string
		seed        func(t *testing.T, targetPath string)
		expectedErr error
	}{
		{
			desc: "empty target directory",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(targetPath, perm.PrivateDir))
			},
			expectedErr: func() error {
				if testhelper.IsWALEnabled() {
					return structerr.NewInternal("begin transaction: get partition: get partition ID: validate git directory: invalid git directory")
				}

				return structerr.NewAlreadyExists("creating fork: repository exists already")
			}(),
		},
		{
			desc: "non-empty target directory",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(targetPath, perm.PrivateDir))
				require.NoError(t, os.WriteFile(
					filepath.Join(targetPath, "config"),
					nil,
					perm.SharedFile,
				))
			},
			expectedErr: func() error {
				if testhelper.IsWALEnabled() {
					return structerr.NewInternal("begin transaction: get partition: get partition ID: validate git directory: invalid git directory")
				}

				return structerr.NewAlreadyExists("creating fork: repository exists already")
			}(),
		},
		{
			desc: "target file",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), perm.PrivateDir))
				require.NoError(t, os.WriteFile(targetPath, nil, perm.SharedFile))
			},
			expectedErr: func() error {
				if testhelper.IsWALEnabled() {
					return structerr.NewInternal("begin transaction: get partition: get partition ID: validate git directory: not a directory")
				}

				return structerr.NewAlreadyExists("creating fork: repository exists already")
			}(),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			cfg, client := setupRepositoryService(t)
			ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

			repo, _ := gittest.CreateRepository(t, ctx, cfg)

			forkedRepo := &gitalypb.Repository{
				// As this test can run with Praefect in front of it, we'll use the next replica path Praefect will
				// assign in order to ensure this repository creation conflicts even with Praefect in front of it.
				// As the source repository created in the setup is the first one, this would get the repository
				// ID 2.
				RelativePath: storage.DeriveReplicaPath(2),
				StorageName:  repo.StorageName,
			}

			tc.seed(t, filepath.Join(cfg.Storages[0].Path, forkedRepo.GetRelativePath()))

			_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
				Repository:       forkedRepo,
				SourceRepository: repo,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestCreateFork_validate(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, cli := setupRepositoryService(t)

	srcRepo, _ := gittest.CreateRepository(t, ctx, cfg)
	// Praefect does not rewrite the SourceRepository storage name, confirm
	// we accept a storage name unknown to Gitaly.
	srcRepo.StorageName = "RailsStorageName"

	for _, tc := range []struct {
		desc        string
		req         *gitalypb.CreateForkRequest
		expectedErr error
	}{
		{
			desc: "repository not provided",
			req: &gitalypb.CreateForkRequest{
				Repository:       nil,
				SourceRepository: srcRepo,
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "source repository not provided",
			req: &gitalypb.CreateForkRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: gittest.NewRepositoryName(t),
				},
				SourceRepository: nil,
			},
			expectedErr: structerr.NewInvalidArgument("validating source repository: %w", storage.ErrRepositoryNotSet),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := cli.CreateFork(ctx, tc.req)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
