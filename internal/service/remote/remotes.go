package remote

import (
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
)

// AddRemote adds a remote to the repository
func (s *server) AddRemote(ctx context.Context, req *pb.AddRemoteRequest) (*pb.AddRemoteResponse, error) {
	if err := validateAddRemoteRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "AddRemote: %v", err)
	}

	client, err := s.RemoteServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.AddRemote(clientCtx, req)
}

func validateAddRemoteRequest(req *pb.AddRemoteRequest) error {
	if strings.TrimSpace(req.GetName()) == "" {
		return fmt.Errorf("empty remote name")
	}
	if req.GetUrl() == "" {
		return fmt.Errorf("empty remote url")
	}

	return nil
}

// RemoveRemote removes the given remote
func (s *server) RemoveRemote(ctx context.Context, req *pb.RemoveRemoteRequest) (*pb.RemoveRemoteResponse, error) {
	if err := validateRemoveRemoteRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "AddRemote: %v", err)
	}

	client, err := s.RemoteServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.RemoveRemote(clientCtx, req)
}

func validateRemoveRemoteRequest(req *pb.RemoveRemoteRequest) error {
	if req.GetName() == "" {
		return fmt.Errorf("empty remote name")
	}

	return nil
}
