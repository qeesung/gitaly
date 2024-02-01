package repository

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func TestApplyGitattributes_successful(t *testing.T) {
	t.Parallel()
	t.Skip("skipping test: ApplyGitattributes is deprecated in git 2.43.0+")

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gitattributesContent := "pattern attr=value"
	commitWithGitattributes := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: gitattributesContent},
	))
	commitWithoutGitattributes := gittest.WriteCommit(t, cfg, repoPath)

	infoPath := filepath.Join(repoPath, "info")
	attributesPath := filepath.Join(infoPath, "attributes")

	for _, tc := range []struct {
		desc            string
		revision        []byte
		expectedContent []byte
	}{
		{
			desc:            "With a .gitattributes file",
			revision:        []byte(commitWithGitattributes),
			expectedContent: []byte(gitattributesContent),
		},
		{
			desc:            "Without a .gitattributes file",
			revision:        []byte(commitWithoutGitattributes),
			expectedContent: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Run("without 'info' directory", func(t *testing.T) {
				require.NoError(t, os.RemoveAll(infoPath))
				requireApplyGitattributes(t, ctx, client, repo, attributesPath, tc.revision, tc.expectedContent)
			})

			t.Run("without 'info/attributes' directory", func(t *testing.T) {
				require.NoError(t, os.RemoveAll(infoPath))
				require.NoError(t, os.Mkdir(infoPath, perm.SharedDir))
				requireApplyGitattributes(t, ctx, client, repo, attributesPath, tc.revision, tc.expectedContent)
			})

			t.Run("with preexisting 'info/attributes'", func(t *testing.T) {
				require.NoError(t, os.RemoveAll(infoPath))
				require.NoError(t, os.Mkdir(infoPath, perm.SharedDir))
				require.NoError(t, os.WriteFile(attributesPath, []byte("*.docx diff=word"), perm.SharedFile))
				requireApplyGitattributes(t, ctx, client, repo, attributesPath, tc.revision, tc.expectedContent)
			})
		})
	}
}

type testTransactionServer struct {
	gitalypb.UnimplementedRefTransactionServer
	vote func(*gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
}

func (s *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	if s.vote != nil {
		return s.vote(in)
	}
	return nil, nil
}

func TestApplyGitattributes_transactional(t *testing.T) {
	t.Parallel()
	t.Skip("skipping test: ApplyGitattributes is deprecated in git 2.43.0+")

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gitattributesContent := "pattern attr=value"
	commitWithGitattributes := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: gitattributesContent},
	))
	commitWithoutGitattributes := gittest.WriteCommit(t, cfg, repoPath)

	transactionServer := &testTransactionServer{}
	runRepositoryService(t, cfg)

	// We're using internal listener in order to route around
	// Praefect in our tests. Otherwise Praefect would replace our
	// carefully crafted transaction and server information.
	logger := testhelper.SharedLogger(t)

	client := newMuxedRepositoryClient(t, ctx, cfg, "unix://"+cfg.InternalSocketPath(),
		backchannel.NewClientHandshaker(
			logger,
			func() backchannel.Server {
				srv := grpc.NewServer()
				gitalypb.RegisterRefTransactionServer(srv, transactionServer)
				return srv
			},
			backchannel.DefaultConfiguration(),
		),
	)

	for _, tc := range []struct {
		desc          string
		revision      []byte
		voteFn        func(*testing.T, *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
		shouldExist   bool
		expectedErr   error
		expectedVotes int
	}{
		{
			desc:     "successful vote writes gitattributes",
			revision: []byte(commitWithGitattributes),
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				vote := voting.VoteFromData([]byte(gitattributesContent))
				expectedHash := vote.Bytes()

				require.Equal(t, expectedHash, request.ReferenceUpdatesHash)
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
			shouldExist:   true,
			expectedVotes: 2,
		},
		{
			desc:     "aborted vote does not write gitattributes",
			revision: []byte(commitWithGitattributes),
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_ABORT,
				}, nil
			},
			shouldExist: false,
			expectedErr: func() error {
				return structerr.NewInternal("committing gitattributes: voting on locked file: preimage vote: transaction was aborted")
			}(),
			expectedVotes: 1,
		},
		{
			desc:     "failing vote does not write gitattributes",
			revision: []byte(commitWithGitattributes),
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return nil, structerr.NewFailedPrecondition("foobar")
			},
			shouldExist: false,
			expectedErr: func() error {
				return structerr.NewFailedPrecondition("committing gitattributes: voting on locked file: preimage vote: rpc error: code = FailedPrecondition desc = foobar")
			}(),
			expectedVotes: 1,
		},
		{
			desc:     "commit without gitattributes performs vote",
			revision: []byte(commitWithoutGitattributes),
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				require.Equal(t, bytes.Repeat([]byte{0x00}, 20), request.ReferenceUpdatesHash)
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
			shouldExist:   false,
			expectedVotes: 2,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			infoPath := filepath.Join(repoPath, "info")
			require.NoError(t, os.RemoveAll(infoPath))

			ctx, err := txinfo.InjectTransaction(ctx, 1, "primary", true)
			require.NoError(t, err)
			ctx = metadata.IncomingToOutgoing(ctx)

			var votes int
			transactionServer.vote = func(request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				votes++
				return tc.voteFn(t, request)
			}

			//nolint:staticcheck
			_, err = client.ApplyGitattributes(ctx, &gitalypb.ApplyGitattributesRequest{
				Repository: repo,
				Revision:   tc.revision,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)

			path := filepath.Join(infoPath, "attributes")
			if tc.shouldExist {
				content := testhelper.MustReadFile(t, path)
				require.Equal(t, []byte(gitattributesContent), content)
			} else {
				require.NoFileExists(t, path)
			}
			require.Equal(t, tc.expectedVotes, votes)
		})
	}
}

func TestApplyGitattributes_failure(t *testing.T) {
	t.Parallel()
	t.Skip("skipping test: ApplyGitattributes is deprecated in git 2.43.0+")

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	for _, tc := range []struct {
		desc        string
		repo        *gitalypb.Repository
		revision    []byte
		expectedErr error
	}{
		{
			desc:        "no repository provided",
			repo:        nil,
			revision:    nil,
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "unknown storage provided",
			repo: &gitalypb.Repository{
				RelativePath: "stub",
				StorageName:  "foo",
			},
			revision: []byte("master"),
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("foo"),
			)),
		},
		{
			desc: "storage not provided",
			repo: &gitalypb.Repository{
				RelativePath: repo.GetRelativePath(),
			},
			revision:    []byte("master"),
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet),
		},
		{
			desc: "repository doesn't exist on disk",
			repo: &gitalypb.Repository{
				StorageName:  repo.GetStorageName(),
				RelativePath: "bar",
			},
			revision: []byte("master"),
			expectedErr: testhelper.ToInterceptedMetadata(
				structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "bar")),
			),
		},
		{
			desc:        "no revision provided",
			repo:        repo,
			revision:    []byte(""),
			expectedErr: structerr.NewInvalidArgument("revision: empty revision"),
		},
		{
			desc:        "unknown revision",
			repo:        repo,
			revision:    []byte("not-existing-ref"),
			expectedErr: structerr.NewInvalidArgument("revision does not exist"),
		},
		{
			desc:        "invalid revision",
			repo:        repo,
			revision:    []byte("--output=/meow"),
			expectedErr: structerr.NewInvalidArgument("revision: revision can't start with '-'"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			//nolint:staticcheck
			_, err := client.ApplyGitattributes(ctx, &gitalypb.ApplyGitattributesRequest{
				Repository: tc.repo,
				Revision:   tc.revision,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func requireApplyGitattributes(
	t *testing.T,
	ctx context.Context,
	client gitalypb.RepositoryServiceClient,
	repo *gitalypb.Repository,
	attributesPath string,
	revision, expectedContent []byte,
) {
	t.Helper()

	//nolint:staticcheck
	response, err := client.ApplyGitattributes(ctx, &gitalypb.ApplyGitattributesRequest{
		Repository: repo,
		Revision:   revision,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.ApplyGitattributesResponse{}, response)

	if expectedContent == nil {
		require.NoFileExists(t, attributesPath)
	} else {
		require.Equal(t, expectedContent, testhelper.MustReadFile(t, attributesPath))

		info, err := os.Stat(attributesPath)
		require.NoError(t, err)
		require.Equal(t, attributesFileMode, info.Mode())
	}
}
