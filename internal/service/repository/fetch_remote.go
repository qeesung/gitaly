package repository

import (
	"fmt"

	log "github.com/Sirupsen/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

func (server) FetchRemote(ctx context.Context, in *pb.FetchRemoteRequest) (*pb.FetchRemoteResponse, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"Remote":     in.GetRemote(),
		"Force":      in.GetForce(),
		"NoTags":     in.GetNoTags(),
		"Timeout":    in.GetTimeout(),
		"SSHKey":     in.GetSshKey(),
		"KnownHosts": in.GetKnownHosts(),
	}).Debug("FetchRemote")

	args, envs, err := fetchRemoteArgBuilder(in)
	if err != nil {
		return nil, err
	}

	cmd, err := command.GitlabShell(ctx, envs, "gitlab-projects", args...)
	if err != nil {
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	if err = cmd.Wait(); err != nil {
		return nil, grpc.Errorf(codes.Internal, err.Error())
	}

	return &pb.FetchRemoteResponse{}, nil
}

// Builds argument and environment for FetchRemote.
// returns ARGS, ENVS, gRPC Error
func fetchRemoteArgBuilder(in *pb.FetchRemoteRequest) ([]string, []string, error) {
	storagePath, ok := config.StoragePath(in.GetRepository().GetStorageName())
	if !ok {
		return nil, nil, grpc.Errorf(codes.NotFound, "Storage not found: %q", in.GetRepository().GetStorageName())
	}

	args := []string{"fetch-remote", storagePath, in.GetRepository().GetRelativePath(), in.GetRemote()}
	if in.GetTimeout() != 0 {
		args = append(args, fmt.Sprintf("%d", in.GetTimeout()))
	}
	if in.GetForce() {
		args = append(args, "--force")
	}
	if in.GetNoTags() {
		args = append(args, "--no-tags")
	}

	var envs []string
	if len(in.GetSshKey()) != 0 {
		envs = append(envs, fmt.Sprintf("GITLAB_SHELL_SSH_KEY=%s", in.GetSshKey()))
	}
	if len(in.GetKnownHosts()) != 0 {
		envs = append(envs, fmt.Sprintf("GITLAB_SHELL_KNOWN_HOSTS=%s", in.GetKnownHosts()))
	}

	return args, envs, nil
}
