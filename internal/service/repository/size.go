package repository

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os/exec"
	"strconv"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
)

func (s *server) RepositorySize(ctx context.Context, in *pb.RepositorySizeRequest) (*pb.RepositorySizeResponse, error) {
	path, err := helper.GetPath(in.Repository)
	if err != nil {
		return nil, err
	}

	cmd, err := command.New(ctx, exec.Command("du", "-sk", path), nil, nil, nil)
	if err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Warn("ignoring du command error")
		return repositorySizeResponse(0), nil
	}

	sizeLine, err := ioutil.ReadAll(cmd)
	if err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Warn("ignoring command read error")
		return repositorySizeResponse(0), nil
	}

	if err := cmd.Wait(); err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Warn("ignoring du wait error")
		return repositorySizeResponse(0), nil
	}

	sizeParts := bytes.Split(sizeLine, []byte("\t"))
	if len(sizeParts) != 2 {
		grpc_logrus.Extract(ctx).Warn(fmt.Sprintf("ignoring du malformed output: %q", sizeLine))
		return repositorySizeResponse(0), nil
	}

	size, err := strconv.ParseInt(string(sizeParts[0]), 10, 0)
	if err != nil {
		grpc_logrus.Extract(ctx).WithError(err).Warn("ignoring parsing size error")
		return repositorySizeResponse(0), nil
	}

	return repositorySizeResponse(size), nil
}

func repositorySizeResponse(size int64) *pb.RepositorySizeResponse {
	return &pb.RepositorySizeResponse{Size: size}
}
