package praefect

import (
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/proxy"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
)

// ReplicateRepositoryHandler intercepts ReplicateRepository RPC calls.
func ReplicateRepositoryHandler(coordinator *Coordinator) grpc.StreamHandler {
	return func(srv interface{}, stream grpc.ServerStream) error {
		ctx := stream.Context()

		// Peek the stream to get first request.
		var req gitalypb.ReplicateRepositoryRequest
		if err := stream.RecvMsg(&req); err != nil {
			return fmt.Errorf("receive request: %w", err)
		}

		// Validate required target repository information is present in first request.
		if err := coordinator.validateTargetRepo(req.GetRepository()); err != nil {
			return structerr.NewInvalidArgument("%w", err)
		}

		const fullMethodName = "/gitaly.RepositoryService/ReplicateRepository"
		mi, err := coordinator.registry.LookupMethod(fullMethodName)
		if err != nil {
			return err
		}

		// Object pool replication is not yet supported by Praefect. Rewrite the request to always
		// disable object pool replication.
		req.ReplicateObjectDeduplicationNetworkMembership = false

		// Generate stream parameters used to configure the stream proxy.
		parameters, err := coordinator.mutatorStreamParameters(ctx, grpcCall{
			fullMethodName: fullMethodName,
			methodInfo:     mi,
			msg:            &req,
			targetRepo:     req.GetRepository(),
		})
		if err != nil {
			return err
		}

		// Proxy stream to destination storages.
		return proxy.HandleStream(stream, fullMethodName, parameters)
	}
}
