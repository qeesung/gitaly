package client

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/gitalyclient"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
)

// UploadPackWithSidechannel proxies an SSH git-upload-pack (git fetch) session to Gitaly

func uploadPackWithSidechannel(ctx context.Context, conn *grpc.ClientConn, reg *SidechannelRegistry, stdin io.Reader, stdout, stderr io.Writer, req *gitalypb.SSHUploadPackWithSidechannelRequest) (UploadPackResult, error) {
	return UploadPackWithSidechannelWithResult(ctx, conn, reg, stdin, stdout, stderr, req)
}


// UploadPack proxies an SSH git-upload-pack (git fetch) session to Gitaly
func UploadPack(ctx context.Context, conn *grpc.ClientConn, stdin io.Reader, stdout, stderr io.Writer, req *gitalypb.SSHUploadPackRequest) (int32, error) {
	return gitalyclient.UploadPack(ctx, conn, stdin, stdout, stderr, req)
}

// UploadPackResult wraps ExitCode and PackfileNegotiationStatistics.
type UploadPackResult = gitalyclient.UploadPackResult

// UploadPackWithSidechannelWithResult proxies an SSH git-upload-pack (git fetch)
// session to Gitaly using a sidechannel for the raw data transfer.
func UploadPackWithSidechannelWithResult(
	ctx context.Context,
	conn *grpc.ClientConn,
	reg *SidechannelRegistry,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req *gitalypb.SSHUploadPackWithSidechannelRequest,
) (UploadPackResult, error) {
	return gitalyclient.UploadPackWithSidechannel(ctx, conn, reg.registry, stdin, stdout, stderr, req)
}

// UploadPackWithSidechannel proxies an SSH git-upload-pack (git fetch)
// session to Gitaly using a sidechannel for the raw data transfer.
// Deprecated: use UploadPackWithSidechannelWithResult instead.
func UploadPackWithSidechannel(
	ctx context.Context,
	conn *grpc.ClientConn,
	reg *SidechannelRegistry,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req *gitalypb.SSHUploadPackWithSidechannelRequest,
) (int32, error) {
	result, err := UploadPackWithSidechannelWithResult(ctx, conn, reg, stdin, stdout, stderr, req)
	if err != nil {
		return 0, err
	}

	return result.ExitCode, err
}
