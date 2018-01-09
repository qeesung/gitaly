package commit

import (
	"io"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
)

func TestExtractCommitSignatureSuccess(t *testing.T) {
	signature5937ac0a7beb003549fc5fd26fc247adbce4a52e, err := ioutil.ReadFile("testdata/commit-5937ac0a7beb003549fc5fd26fc247adbce4a52e-signature")
	require.NoError(t, err)

	signedText5937ac0a7beb003549fc5fd26fc247adbce4a52e, err := ioutil.ReadFile("testdata/commit-5937ac0a7beb003549fc5fd26fc247adbce4a52e-signed-text")
	require.NoError(t, err)

	ctx, cancel := testhelper.Context()
	defer cancel()

	server, serverSocketPath := startTestServices(t)
	defer server.Stop()

	client, conn := newCommitServiceClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		desc       string
		req        *pb.ExtractCommitSignatureRequest
		signature  []byte
		signedText []byte
	}{
		{
			desc: "commit with signature",
			req: &pb.ExtractCommitSignatureRequest{
				Repository: testRepo,
				CommitId:   "5937ac0a7beb003549fc5fd26fc247adbce4a52e",
			},
			signature:  signature5937ac0a7beb003549fc5fd26fc247adbce4a52e,
			signedText: signedText5937ac0a7beb003549fc5fd26fc247adbce4a52e,
		},
		{
			desc: "commit without signature",
			req: &pb.ExtractCommitSignatureRequest{
				Repository: testRepo,
				CommitId:   "e63f41fe459e62e1228fcef60d7189127aeba95a",
			},
			signature:  nil,
			signedText: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			signature, signedText, err := getSignatureAndText(ctx, client, tc.req)
			require.NoError(t, err)

			require.Equal(t, string(tc.signature), string(signature))
			require.Equal(t, string(tc.signedText), string(signedText))
		})
	}
}

func getSignatureAndText(ctx context.Context, client pb.CommitServiceClient, req *pb.ExtractCommitSignatureRequest) ([]byte, []byte, error) {
	stream, err := client.ExtractCommitSignature(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	var signature, signedText []byte
	var resp *pb.ExtractCommitSignatureResponse
	for err == nil {
		resp, err = stream.Recv()
		if err != nil && err != io.EOF {
			return nil, nil, err
		}

		signature = append(signature, resp.GetSignature()...)
		signedText = append(signedText, resp.GetSignedText()...)
	}

	return signature, signedText, nil
}

func TestExtractCommitSignatureFail(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	server, serverSocketPath := startTestServices(t)
	defer server.Stop()

	client, conn := newCommitServiceClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		desc string
		req  *pb.ExtractCommitSignatureRequest
		code codes.Code
	}{
		{
			desc: "truncated commit ID",
			req: &pb.ExtractCommitSignatureRequest{
				Repository: testRepo,
				CommitId:   "5937ac0a7beb003549fc5fd26",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit ID",
			req: &pb.ExtractCommitSignatureRequest{
				Repository: testRepo,
				CommitId:   "",
			},
			code: codes.InvalidArgument,
		}}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.ExtractCommitSignature(ctx, tc.req)
			require.NoError(t, err)

			for err == nil {
				_, err = stream.Recv()
			}

			testhelper.AssertGrpcError(t, err, tc.code, "")
		})
	}
}
