package gitalyclient

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/stream"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
)

// UploadPack proxies an SSH git-upload-pack (git fetch) session to Gitaly
func UploadPack(ctx context.Context, conn *grpc.ClientConn, stdin io.Reader, stdout, stderr io.Writer, req *gitalypb.SSHUploadPackRequest) (int32, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ssh := gitalypb.NewSSHServiceClient(conn)
	uploadPackStream, err := ssh.SSHUploadPack(ctx)
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
	PackfileNegotiationStatistics *gitalypb.PackfileNegotiationStatistics
}

// UploadPackWithSidechannel proxies an SSH git-upload-pack (git fetch) session to Gitaly using a sidechannel for the
// raw data transfer.
func UploadPackWithSidechannel(
	ctx context.Context,
	conn *grpc.ClientConn,
	reg *sidechannel.Registry,
	stdin io.Reader,
	stdout, stderr io.Writer,
	req *gitalypb.SSHUploadPackWithSidechannelRequest,
) (UploadPackResult, error) {
	result := UploadPackResult{}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctx, wt := sidechannel.RegisterSidechannel(ctx, reg, func(c *sidechannel.ClientConn) error {
		return stream.ProxyPktLine(c, stdin, stdout, stderr)
	})
	defer func() {
		// We already check the error further down.
		_ = wt.Close()
	}()

	sshClient := gitalypb.NewSSHServiceClient(conn)
	resp, err := sshClient.SSHUploadPackWithSidechannel(ctx, req)
	if err != nil {
		return result, err
	}
	result.ExitCode = 0
	result.PackfileNegotiationStatistics = resp.PackfileNegotiationStatistics

	if err := wt.Close(); err != nil {
		return result, err
	}

	return result, nil
}
