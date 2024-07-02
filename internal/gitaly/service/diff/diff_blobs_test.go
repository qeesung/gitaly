package diff

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestDiffBlobs(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	type setupData struct {
		request           *gitalypb.DiffBlobsRequest
		expectedResponses []*gitalypb.DiffBlobsResponse
		expectedErr       error
	}

	for _, tc := range []struct {
		setup func() setupData
		desc  string
	}{
		{
			desc: "invalid repository in request",
			setup: func() setupData {
				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: nil,
					},
					expectedErr: structerr.NewInvalidArgument("repository not set"),
				}
			},
		},
		{
			desc: "invalid blob pair in request",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID),
								RightBlob: nil,
							},
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						fmt.Sprintf(
							"validating right blob: validating blob ID: invalid object ID: \"\", expected length %d, got 0",
							gittest.DefaultObjectHash.EncodedLen(),
						)).WithMetadata("revision", ""),
					),
				}
			},
		},
		{
			desc: "commit ID in request",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID),
								RightBlob: []byte(commitID),
							},
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"validating right blob: revision is not blob").WithMetadata("revision", string(commitID)),
					),
				}
			},
		},
		{
			desc: "not found path scoped blob revision in request",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte("HEAD:foo"),
								RightBlob: []byte(blobID),
							},
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"validating left blob: getting revision info: object not found").WithMetadata("revision", "HEAD:foo"),
					),
				}
			},
		},
		{
			desc: "path scoped blob revision in request",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{
						OID:  blobID1,
						Mode: "100644",
						Path: "foo",
					},
				))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(fmt.Sprintf("%s:foo", commitID.String())),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n+bar\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "path scoped blob revision in request with attributes applied",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{
						OID:  blobID1,
						Mode: "100644",
						Path: "foo",
					},
					gittest.TreeEntry{
						Mode:    "100644",
						Path:    ".gitattributes",
						Content: "foo binary",
					},
				))

				expectedResponse := []*gitalypb.DiffBlobsResponse{
					{
						LeftBlobId:  blobID1.String(),
						RightBlobId: blobID2.String(),
						Patch:       []byte(fmt.Sprintf("Binary files a/foo and b/%s differ\n", blobID2.String())),
						Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						Binary:      true,
					},
				}

				if !featureflag.SetAttrTreeConfig.IsEnabled(ctx) {
					version, err := gittest.NewCommandFactory(t, cfg).GitVersion(ctx)
					require.NoError(t, err)

					if version.GreaterOrEqual(git.NewVersion(2, 46, 0, 0)) {
						expectedResponse[0].Binary = false
						expectedResponse[0].Patch = []byte("@@ -1 +1 @@\n-foo\n+bar\n")
					}
				}

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte("HEAD:foo"),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: expectedResponse,
				}
			},
		},
		{
			desc: "single blob pair diffed",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n+bar\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "multiple blob pairs diffed",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
							{
								LeftBlob:  []byte(blobID2),
								RightBlob: []byte(blobID1),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n+bar\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
						{
							LeftBlobId:  blobID2.String(),
							RightBlobId: blobID1.String(),
							Patch:       []byte("@@ -1 +1 @@\n-bar\n+foo\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "single blob pair diff chunked across responses",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// Create large blobs that when diffed will span across response messages. The 14
				// byte offset here nicely aligns the chunks to make validation easier.
				data1 := strings.Repeat("f", msgSizeThreshold-14) + "\n"
				data2 := strings.Repeat("b", msgSizeThreshold-14) + "\n"

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte(data1))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte(data2))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte(fmt.Sprintf("@@ -1 +1 @@\n-%s", data1)),
						},
						{
							Patch:  []byte(fmt.Sprintf("+%s", data2)),
							Status: gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "binary blob pair diffed",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("\x000 foo"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("\x000 bar"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch: []byte(fmt.Sprintf("Binary files a/%s and b/%s differ\n",
								[]byte(blobID1),
								[]byte(blobID2),
							)),
							Binary: true,
							Status: gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "word diff computed",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo bar baz\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo bob baz\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						DiffMode:   gitalypb.DiffBlobsRequest_DIFF_MODE_WORD,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n foo \n-bar\n+bob\n  baz\n~\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "whitespace_changes: dont_ignore",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo \n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n+foo \n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "whitespace_changes: ignore",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo \n"))
				// Prefix space is not ignored.
				blobID3 := gittest.WriteBlob(t, cfg, repoPath, []byte(" foo \n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository:        repoProto,
						WhitespaceChanges: gitalypb.DiffBlobsRequest_WHITESPACE_CHANGES_IGNORE,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID3),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID3.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n+ foo \n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "whitespace_changes: ignore_all",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo \n"))
				blobID3 := gittest.WriteBlob(t, cfg, repoPath, []byte(" foo \n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository:        repoProto,
						WhitespaceChanges: gitalypb.DiffBlobsRequest_WHITESPACE_CHANGES_IGNORE_ALL,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID3),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID3.String(),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "blobs exceeding core.bigFileThreshold",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// The blobs are crafted such that the huge common data will not be in the context of
				// the diff anymore to make this a bit more efficient.
				data1 := strings.Repeat("1", 50*1024*1024) + "\n\n\n\na\n"
				data2 := strings.Repeat("1", 50*1024*1024) + "\n\n\n\nb\n"

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte(data1))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte(data2))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch: []byte(fmt.Sprintf("Binary files a/%s and b/%s differ\n",
								[]byte(blobID1),
								[]byte(blobID2),
							)),
							Status: gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
							Binary: true,
						},
					},
				}
			},
		},
		{
			desc: "no newline at the end",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +1 @@\n-foo\n\\ No newline at end of file\n+bar\n\\ No newline at end of file\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "null left blob ID",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.DefaultObjectHash.ZeroOID
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -0,0 +1 @@\n+bar\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "null right blob ID",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foo\n"))
				blobID2 := gittest.DefaultObjectHash.ZeroOID

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -1 +0,0 @@\n-foo\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
		{
			desc: "null left and right blob ID",
			setup: func() setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.DefaultObjectHash.ZeroOID
				blobID2 := gittest.DefaultObjectHash.ZeroOID

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedErr: structerr.NewInvalidArgument("left and right blob cannot both be null OIDs"),
				}
			},
		},
		{
			desc: "empty blob",
			setup: func() setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte(""))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("bar\n"))

				return setupData{
					request: &gitalypb.DiffBlobsRequest{
						Repository: repoProto,
						BlobPairs: []*gitalypb.DiffBlobsRequest_BlobPair{
							{
								LeftBlob:  []byte(blobID1),
								RightBlob: []byte(blobID2),
							},
						},
					},
					expectedResponses: []*gitalypb.DiffBlobsResponse{
						{
							LeftBlobId:  blobID1.String(),
							RightBlobId: blobID2.String(),
							Patch:       []byte("@@ -0,0 +1 @@\n+bar\n"),
							Status:      gitalypb.DiffBlobsResponse_STATUS_END_OF_PATCH,
						},
					},
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			data := tc.setup()

			stream, err := client.DiffBlobs(ctx, data.request)
			require.NoError(t, err)

			var actualResp []*gitalypb.DiffBlobsResponse
			for {
				resp, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}

				testhelper.RequireGrpcError(t, data.expectedErr, err)
				if err != nil {
					break
				}

				actualResp = append(actualResp, resp)
			}

			testhelper.ProtoEqual(t, data.expectedResponses, actualResp)
		})
	}
}
