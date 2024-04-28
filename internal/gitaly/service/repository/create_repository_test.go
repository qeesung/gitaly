package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/sys/unix"
)

func TestCreateRepository_missingAuth(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Auth: auth.Config{Token: "some"}}))

	_, serverSocketPath := runRepositoryService(t, cfg)
	client := newRepositoryClient(t, config.Cfg{Auth: auth.Config{Token: ""}}, serverSocketPath)

	_, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository: &gitalypb.Repository{
			StorageName:  cfg.Storages[0].Name,
			RelativePath: gittest.NewRepositoryName(t),
		},
	})
	testhelper.RequireGrpcError(t, structerr.NewUnauthenticated("authentication required"), err)
}

func TestCreateRepository_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: gittest.NewRepositoryName(t),
	}

	_, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	repoDir := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))

	require.NoError(t, unix.Access(repoDir, unix.R_OK))
	require.NoError(t, unix.Access(repoDir, unix.W_OK))
	require.NoError(t, unix.Access(repoDir, unix.X_OK))

	for _, dir := range []string{repoDir, filepath.Join(repoDir, "refs")} {
		fi, err := os.Stat(dir)
		require.NoError(t, err)
		require.True(t, fi.IsDir(), "%q must be a directory", fi.Name())

		require.NoError(t, unix.Access(dir, unix.R_OK))
		require.NoError(t, unix.Access(dir, unix.W_OK))
		require.NoError(t, unix.Access(dir, unix.X_OK))
	}

	symRef := string(gittest.Exec(t, cfg, "-C", repoDir, "symbolic-ref", "HEAD"))
	require.Equal(t, symRef, fmt.Sprintf("%s\n", git.DefaultRef))
}

func TestCreateRepository_withDefaultBranch(t *testing.T) {
	t.Parallel()

	cfg, client := setupRepositoryService(t)
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc              string
		defaultBranch     string
		expected          string
		expectedErrString string
	}{
		{
			desc:          "valid default branch",
			defaultBranch: "develop",
			expected:      "refs/heads/develop",
		},
		{
			desc:          "empty branch name",
			defaultBranch: "",
			expected:      "refs/heads/main",
		},
		{
			desc:              "invalid branch name",
			defaultBranch:     "./.lock",
			expected:          "refs/heads/main",
			expectedErrString: `creating repository: exit status 128, stderr: "fatal: invalid initial branch name: './.lock'\n"`,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repo := &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: gittest.NewRepositoryName(t)}

			req := &gitalypb.CreateRepositoryRequest{Repository: repo, DefaultBranch: []byte(tc.defaultBranch)}
			_, err := client.CreateRepository(ctx, req)
			if tc.expectedErrString != "" {
				require.Contains(t, err.Error(), tc.expectedErrString)
			} else {
				require.NoError(t, err)
				repoPath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
				symRef := text.ChompBytes(gittest.Exec(
					t,
					cfg,
					"-C", repoPath,
					"symbolic-ref", "HEAD"))
				require.Equal(t, tc.expected, symRef)
			}
		})
	}
}

func TestCreateRepository_withObjectFormat(t *testing.T) {
	t.Parallel()

	cfg, client := setupRepositoryService(t)
	ctx := testhelper.Context(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)

	for _, tc := range []struct {
		desc               string
		objectFormat       gitalypb.ObjectFormat
		expectedResponse   *gitalypb.CreateRepositoryResponse
		expectedObjectHash git.ObjectHash
		expectedErr        error
	}{
		{
			desc:               "unspecified object format",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_UNSPECIFIED,
			expectedResponse:   &gitalypb.CreateRepositoryResponse{},
			expectedObjectHash: git.ObjectHashSHA1,
		},
		{
			desc:               "SHA1",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_SHA1,
			expectedResponse:   &gitalypb.CreateRepositoryResponse{},
			expectedObjectHash: git.ObjectHashSHA1,
		},
		{
			desc:               "SHA256",
			objectFormat:       gitalypb.ObjectFormat_OBJECT_FORMAT_SHA256,
			expectedResponse:   &gitalypb.CreateRepositoryResponse{},
			expectedObjectHash: git.ObjectHashSHA256,
		},
		{
			desc:         "invalid object format",
			objectFormat: 3,
			expectedErr:  structerr.NewInvalidArgument("unknown object format: \"3\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto := &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: gittest.NewRepositoryName(t),
			}

			response, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
				Repository:   repoProto,
				ObjectFormat: tc.objectFormat,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)

			if err != nil {
				return
			}

			// If the repository was created we can check whether the object format of
			// the created repository matches our expectations.
			objectHash, err := git.DetectObjectHash(ctx, gitCmdFactory, gittest.RewrittenRepository(t, ctx, cfg, repoProto))
			require.NoError(t, err)
			require.Equal(t, tc.expectedObjectHash.Format, objectHash.Format)
		})
	}
}

func TestCreateRepository_invalidArguments(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	preexistingRepo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		// This creates the first repository on the server. As this test can run with
		// Praefect in front of it, we'll use the next replica path Praefect will assign in
		// order to ensure this repository creation conflicts even with Praefect in front of
		// it.
		RelativePath: storage.DeriveReplicaPath(1),
	})

	for _, tc := range []struct {
		desc        string
		repo        *gitalypb.Repository
		expectedErr error
	}{
		{
			desc:        "missing repository",
			repo:        nil,
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "invalid storage",
			repo: &gitalypb.Repository{
				StorageName:  "does not exist",
				RelativePath: "foobar.git",
			},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("does not exist"),
			)),
		},
		{
			desc: "preexisting repository",
			repo: preexistingRepo,
			expectedErr: structerr.NewAlreadyExists(testhelper.GitalyOrPraefect(
				"creating repository: repository exists already",
				"route repository creation: reserve repository id: repository already exists",
			)),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{Repository: tc.repo})
			require.Error(t, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestCreateRepository_transactional(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	txManager := transaction.NewTrackingManager()
	cfg, client := setupRepositoryService(t, testserver.WithTransactionManager(txManager))

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: "repo.git",
	}
	_, err = client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{Repository: repo})
	require.NoError(t, err)

	require.DirExists(t, filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo)))
	require.Equal(t, 2, len(txManager.Votes()), "expected transactional vote")
}
