package repository

import (
	"io"
	"os"
	"os/exec"
	"path"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) RestoreCustomHooks(stream pb.RepositoryService_RestoreCustomHooksServer) error {

	firstRequest, err := stream.Recv()

	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: first request failed %v", err)
	}

	repo := firstRequest.GetRepository()

	if repo == nil {
		return status.Errorf(codes.InvalidArgument, "RestoreCustomHooks: empty Repository")
	}

	firstRead := false

	reader := streamio.NewReader(func() ([]byte, error) {
		if !firstRead {
			firstRead = true
			return firstRequest.GetData(), nil
		}

		request, err := stream.Recv()
		return request.GetData(), err
	})

	ctx := stream.Context()

	tmpDir, err := tempdir.New(ctx, repo)
	tarPath := path.Join(tmpDir, "custom_hooks.tar")

	file, err := os.Create(tarPath)

	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: Can not create file%v", err)
	}
	_, err = io.Copy(file, reader)
	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: write tar file failed %v", err)
	}
	repoPath, err := helper.GetPath(repo)

	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: getting repo path failed %v", err)
	}

	repoCustomHooksPath := path.Join(repoPath, "custom_hooks")

	err = os.Mkdir(repoCustomHooksPath, 0700)

	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: creating custom hooks directory in repo failed %v", err)
	}

	cmdArgs := []string{
		"-xf",
		tarPath,
		"-C",
		repoCustomHooksPath,
	}

	cmd, err := command.New(ctx, exec.Command("tar", cmdArgs...), nil, nil, nil)

	if err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: Could not untar custom hooks tar %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return status.Errorf(codes.Internal, "RestoreCustomHooks: cmd wait failed: %v", err)
	}
	return stream.SendAndClose(&pb.RestoreCustomHooksResponse{})
}
