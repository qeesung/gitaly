package blob

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/protobuf/proto"
)

func TestListBlobs(t *testing.T) {
	// In order to get deterministic behaviour with regards to how streamio splits up values,
	// we're going to limit the write batch size to something smallish. Otherwise, tests will
	// depend on both the batch size of streamio and the batch size of io.Copy.
	defer func(oldValue int) {
		streamio.WriteBufferSize = oldValue
	}(streamio.WriteBufferSize)
	streamio.WriteBufferSize = 200
	ctx := testhelper.Context(t)

	cfg, client := setup(t, ctx)
	repoProto, repoPath, repoInfo := setupRepoWithLFS(t, ctx, cfg)

	blobASize := int64(251)
	blobAData := bytes.Repeat([]byte{'a'}, int(blobASize))
	blobAOID := gittest.WriteBlob(t, cfg, repoPath, blobAData)

	blobBSize := int64(64)
	blobBData := bytes.Repeat([]byte{'b'}, int(blobBSize))
	blobBOID := gittest.WriteBlob(t, cfg, repoPath, blobBData)

	bigBlobData := bytes.Repeat([]byte{1}, streamio.WriteBufferSize*2+1)
	bigBlobOID := gittest.WriteBlob(t, cfg, repoPath, bigBlobData)

	treeOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "a", Mode: "100644", OID: blobAOID},
		{Path: "b", Mode: "100644", OID: blobBOID},
	})

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(repoInfo.defaultCommitID),
		gittest.WithTree(treeOID),
		gittest.WithBranch("master"),
	)

	for _, tc := range []struct {
		desc          string
		revisions     []string
		limit         uint32
		bytesLimit    int64
		withPaths     bool
		expectedErr   error
		expectedBlobs []*gitalypb.ListBlobsResponse_Blob
	}{
		{
			desc:        "missing revisions",
			revisions:   []string{},
			expectedErr: structerr.NewInvalidArgument("missing revisions"),
		},
		{
			desc: "invalid revision",
			revisions: []string{
				"--foobar",
			},
			expectedErr: testhelper.WithInterceptedMetadata(
				structerr.NewInvalidArgument("invalid revision: revision can't start with '-'"),
				"revision", "--foobar",
			),
		},
		{
			desc: "single blob",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
			},
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size},
			},
		},
		{
			desc: "revision and path",
			revisions: []string{
				"master:a",
			},
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize},
			},
		},
		{
			desc: "single blob with paths",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
			},
			withPaths: true,
			// When iterating blobs directly, we cannot deduce a path and thus don't get
			// any as response.
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size},
			},
		},
		{
			desc: "multiple blobs",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
				repoInfo.lfsPointers[1].Oid,
				repoInfo.lfsPointers[2].Oid,
			},
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size},
				{Oid: repoInfo.lfsPointers[1].Oid, Size: repoInfo.lfsPointers[1].Size},
				{Oid: repoInfo.lfsPointers[2].Oid, Size: repoInfo.lfsPointers[2].Size},
			},
		},
		{
			desc: "tree",
			revisions: []string{
				treeOID.String(),
			},
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize},
				{Oid: blobBOID.String(), Size: blobBSize},
			},
		},
		{
			desc: "tree with paths",
			revisions: []string{
				treeOID.String(),
			},
			withPaths: true,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize, Path: []byte("a")},
				{Oid: blobBOID.String(), Size: blobBSize, Path: []byte("b")},
			},
		},
		{
			desc: "revision range",
			revisions: []string{
				"master",
				"^master~",
			},
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize},
				{Oid: blobBOID.String(), Size: blobBSize},
			},
		},
		{
			desc: "pseudorevisions",
			revisions: []string{
				"master",
				"--not",
				"--all",
			},
			expectedBlobs: nil,
		},
		{
			desc: "revision with limit",
			revisions: []string{
				"master",
			},
			limit: 2,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize},
				{Oid: blobBOID.String(), Size: blobBSize},
			},
		},
		{
			desc: "revision with limit and path",
			revisions: []string{
				"master",
			},
			limit:     2,
			withPaths: true,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize, Path: []byte("a")},
				{Oid: blobBOID.String(), Size: blobBSize, Path: []byte("b")},
			},
		},
		{
			desc: "revision with limit and bytes limit",
			revisions: []string{
				"master",
			},
			limit:      2,
			bytesLimit: 5,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobAOID.String(), Size: blobASize, Data: []byte("aaaaa")},
				{Oid: blobBOID.String(), Size: blobBSize, Data: []byte("bbbbb")},
			},
		},
		{
			desc: "revision with path",
			revisions: []string{
				"master:b",
			},
			bytesLimit: -1,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: blobBOID.String(), Size: blobBSize, Data: blobBData},
			},
		},
		{
			desc: "complete contents via negative bytes limit",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
				repoInfo.lfsPointers[1].Oid,
				repoInfo.lfsPointers[2].Oid,
			},
			bytesLimit: -1,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size, Data: repoInfo.lfsPointers[0].Data},
				{Oid: repoInfo.lfsPointers[1].Oid, Size: repoInfo.lfsPointers[1].Size, Data: repoInfo.lfsPointers[1].Data},
				{Oid: repoInfo.lfsPointers[2].Oid, Size: repoInfo.lfsPointers[2].Size, Data: repoInfo.lfsPointers[2].Data},
			},
		},
		{
			desc: "contents truncated by bytes limit",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
				repoInfo.lfsPointers[1].Oid,
				repoInfo.lfsPointers[2].Oid,
			},
			bytesLimit: 10,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size, Data: repoInfo.lfsPointers[0].Data[:10]},
				{Oid: repoInfo.lfsPointers[1].Oid, Size: repoInfo.lfsPointers[1].Size, Data: repoInfo.lfsPointers[1].Data[:10]},
				{Oid: repoInfo.lfsPointers[2].Oid, Size: repoInfo.lfsPointers[2].Size, Data: repoInfo.lfsPointers[2].Data[:10]},
			},
		},
		{
			desc: "bytes limit exceeding total blob content size",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
				repoInfo.lfsPointers[1].Oid,
				repoInfo.lfsPointers[2].Oid,
			},
			bytesLimit: 9000,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size, Data: repoInfo.lfsPointers[0].Data},
				{Oid: repoInfo.lfsPointers[1].Oid, Size: repoInfo.lfsPointers[1].Size, Data: repoInfo.lfsPointers[1].Data},
				{Oid: repoInfo.lfsPointers[2].Oid, Size: repoInfo.lfsPointers[2].Size, Data: repoInfo.lfsPointers[2].Data},
			},
		},
		{
			desc: "bytes limit partially exceeding limit",
			revisions: []string{
				repoInfo.lfsPointers[0].Oid,
				repoInfo.lfsPointers[1].Oid,
				repoInfo.lfsPointers[2].Oid,
			},
			bytesLimit: 128,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size, Data: repoInfo.lfsPointers[0].Data[:128]},
				{Oid: repoInfo.lfsPointers[1].Oid, Size: repoInfo.lfsPointers[1].Size, Data: repoInfo.lfsPointers[1].Data},
				{Oid: repoInfo.lfsPointers[2].Oid, Size: repoInfo.lfsPointers[2].Size, Data: repoInfo.lfsPointers[2].Data},
			},
		},
		{
			desc: "blob with content bigger than a gRPC message",
			revisions: []string{
				bigBlobOID.String(),
				repoInfo.lfsPointers[0].Oid,
			},
			bytesLimit: -1,
			expectedBlobs: []*gitalypb.ListBlobsResponse_Blob{
				{Oid: bigBlobOID.String(), Size: int64(len(bigBlobData)), Data: bigBlobData[:streamio.WriteBufferSize]},
				{Data: bigBlobData[streamio.WriteBufferSize : 2*streamio.WriteBufferSize]},
				{Data: bigBlobData[2*streamio.WriteBufferSize:]},
				{Oid: repoInfo.lfsPointers[0].Oid, Size: repoInfo.lfsPointers[0].Size, Data: repoInfo.lfsPointers[0].Data},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.ListBlobs(ctx, &gitalypb.ListBlobsRequest{
				Repository: repoProto,
				Revisions:  tc.revisions,
				Limit:      tc.limit,
				BytesLimit: tc.bytesLimit,
				WithPaths:  tc.withPaths,
			})
			require.NoError(t, err)

			var blobs []*gitalypb.ListBlobsResponse_Blob
			for {
				resp, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						testhelper.RequireGrpcError(t, tc.expectedErr, err)
					}
					break
				}

				blobs = append(blobs, resp.Blobs...)
			}

			testhelper.ProtoEqual(t, tc.expectedBlobs, blobs)
		})
	}
}

func TestListAllBlobs(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setup(t, ctx)

	repo, _, _ := setupRepoWithLFS(t, ctx, cfg)

	quarantine, _, err := quarantine.New(ctx, gittest.RewrittenRepository(t, ctx, cfg, repo), testhelper.NewLogger(t), config.NewLocator(cfg))
	require.NoError(t, err)

	// quarantine.New in Gitaly would receive an already rewritten repository. Gitaly would then calculate
	// the quarantine directories based on the rewritten relative path. That quarantine would then be looped
	// through Rails, which would then send a request with the quarantine object directories set based on the
	// rewritten relative path but with the original relative path of the repository. Since we're using the production
	// helpers here, we need to manually substitute the rewritten relative path with the original one when sending
	// it back through the API.
	quarantinedRepo := quarantine.QuarantinedRepo()
	quarantinedRepo.RelativePath = repo.RelativePath

	quarantineRepoWithoutAlternates := proto.Clone(quarantinedRepo).(*gitalypb.Repository)
	quarantineRepoWithoutAlternates.GitAlternateObjectDirectories = []string{}

	emptyRepo, _ := gittest.CreateRepository(t, ctx, cfg)

	singleBlobRepo, singleBlobRepoPath := gittest.CreateRepository(t, ctx, cfg)
	blobID := gittest.WriteBlob(t, cfg, singleBlobRepoPath, []byte("foobar"))

	for _, tc := range []struct {
		desc    string
		request *gitalypb.ListAllBlobsRequest
		verify  func(*testing.T, []*gitalypb.ListAllBlobsResponse_Blob)
	}{
		{
			desc: "empty repo",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: emptyRepo,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Empty(t, blobs)
			},
		},
		{
			desc: "repo with single blob",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: singleBlobRepo,
				BytesLimit: -1,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Equal(t, []*gitalypb.ListAllBlobsResponse_Blob{{
					Oid:  blobID.String(),
					Size: 6,
					Data: []byte("foobar"),
				}}, blobs)
			},
		},
		{
			desc: "repo with single blob and bytes limit",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: singleBlobRepo,
				BytesLimit: 1,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Equal(t, []*gitalypb.ListAllBlobsResponse_Blob{{
					Oid:  blobID.String(),
					Size: 6,
					Data: []byte("f"),
				}}, blobs)
			},
		},
		{
			desc: "normal repo",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: repo,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Len(t, blobs, 6)
			},
		},
		{
			desc: "normal repo with limit",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: repo,
				Limit:      2,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Len(t, blobs, 2)
			},
		},
		{
			desc: "normal repo with bytes limit",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: repo,
				BytesLimit: 1,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Equal(t, len(blobs), 6)

				for _, blob := range blobs {
					emptyBlobID := "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
					if blob.Oid == emptyBlobID {
						require.Empty(t, blob.Data)
						require.Equal(t, int64(0), blob.Size)
					} else {
						require.Len(t, blob.Data, 1)
					}
				}
			},
		},
		{
			desc: "quarantine repo with alternates",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: quarantinedRepo,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Len(t, blobs, 6)
			},
		},
		{
			desc: "quarantine repo without alternates",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: quarantineRepoWithoutAlternates,
			},
			verify: func(t *testing.T, blobs []*gitalypb.ListAllBlobsResponse_Blob) {
				require.Empty(t, blobs)
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			stream, err := client.ListAllBlobs(ctx, tc.request)
			require.NoError(t, err)

			var blobs []*gitalypb.ListAllBlobsResponse_Blob
			for {
				resp, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						require.NoError(t, err)
					}
					break
				}

				blobs = append(blobs, resp.Blobs...)
			}

			tc.verify(t, blobs)
		})
	}
}

func BenchmarkListAllBlobs(b *testing.B) {
	b.StopTimer()
	ctx := testhelper.Context(b)

	cfg, client := setup(b, ctx)
	repoProto, _, _ := setupRepoWithLFS(b, ctx, cfg)

	for _, tc := range []struct {
		desc    string
		request *gitalypb.ListAllBlobsRequest
	}{
		{
			desc: "with contents",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: repoProto,
				BytesLimit: -1,
			},
		},
		{
			desc: "without contents",
			request: &gitalypb.ListAllBlobsRequest{
				Repository: repoProto,
				BytesLimit: 0,
			},
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				stream, err := client.ListAllBlobs(ctx, tc.request)
				require.NoError(b, err)

				for {
					_, err := stream.Recv()
					if err == io.EOF {
						break
					}
					require.NoError(b, err)
				}
			}
		})
	}
}

func BenchmarkListBlobs(b *testing.B) {
	b.StopTimer()
	ctx := testhelper.Context(b)

	cfg, client := setup(b, ctx)
	repoProto, _, _ := setupRepoWithLFS(b, ctx, cfg)

	for _, tc := range []struct {
		desc    string
		request *gitalypb.ListBlobsRequest
	}{
		{
			desc: "with contents",
			request: &gitalypb.ListBlobsRequest{
				Repository: repoProto,
				Revisions:  []string{"refs/heads/master"},
				BytesLimit: -1,
			},
		},
		{
			desc: "without contents",
			request: &gitalypb.ListBlobsRequest{
				Repository: repoProto,
				Revisions:  []string{"refs/heads/master"},
				BytesLimit: 0,
			},
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				stream, err := client.ListBlobs(ctx, tc.request)
				require.NoError(b, err)

				for {
					_, err := stream.Recv()
					if err == io.EOF {
						break
					}
					require.NoError(b, err)
				}
			}
		})
	}
}
