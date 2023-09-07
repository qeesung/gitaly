package client

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
)

// UploadPack proxies an SSH git-upload-pack (git fetch) session to Gitaly
func UploadPack(ctx context.Context, conn *grpc.ClientConn, stdin io.Reader, stdout, stderr io.Writer, req *gitalypb.SSHUploadPackRequest) (int32, error) {
	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	ssh := gitalypb.NewSSHServiceClient(conn)
	uploadPackStream, err := ssh.SSHUploadPack(ctx2)
	if err != nil {
		return 0, err
	}

	if err = uploadPackStream.Send(req); err != nil {
		return 0, err
	}

	inWriter := streamio.NewWriter(func(p []byte) error {
		return uploadPackStream.Send(&gitalypb.SSHUploadPackRequest{Stdin: p})
	})

	return stream.Handler(func() (stream.StdoutStderrResponse, error) {
		return uploadPackStream.Recv()
	}, func(errC chan error) {
		_, errRecv := io.Copy(inWriter, stdin)
		if err := uploadPackStream.CloseSend(); err != nil && errRecv == nil {
			errC <- err
		} else {
			errC <- errRecv
		}
	}, stdout, stderr)
}

// UploadPackResult wraps ExitCode and PackfileNegotiationStatistics.
type UploadPackResult struct {
	ExitCode                      int32
	ResponseBytes                 int64
	PackfileNegotiationStatistics *gitalypb.PackfileNegotiationStatistics
}

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
	result := UploadPackResult{}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctx, wt := reg.Register(ctx, func(c SidechannelConn) error {
		return stream.ProxyPktLine(c, stdin, stdout, stderr)
	})
	defer func() {
		// We aleady check the error further down.
		_ = wt.Close()
	}()

	sshClient := gitalypb.NewSSHServiceClient(conn)
	resp, err := sshClient.SSHUploadPackWithSidechannel(ctx, req)
	if err != nil {
		return result, err
	}
	result.ExitCode = 0
	result.ResponseBytes = resp.GetBytes()
	result.PackfileNegotiationStatistics = resp.PackfileNegotiationStatistics

	if err := wt.Close(); err != nil {
		return result, err
	}

	return result, nil
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
