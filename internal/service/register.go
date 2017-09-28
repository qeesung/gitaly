package service

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/service/blob"
	"gitlab.com/gitlab-org/gitaly/internal/service/commit"
	"gitlab.com/gitlab-org/gitaly/internal/service/diff"
	"gitlab.com/gitlab-org/gitaly/internal/service/namespace"
	"gitlab.com/gitlab-org/gitaly/internal/service/notifications"
	"gitlab.com/gitlab-org/gitaly/internal/service/operations"
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/service/repository"
	"gitlab.com/gitlab-org/gitaly/internal/service/smarthttp"
	"gitlab.com/gitlab-org/gitaly/internal/service/ssh"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterAll will register all the known grpc services with
// the specified grpc service instance
func RegisterAll(grpcServer *grpc.Server, rubyServer *rubyserver.Server) {
	pb.RegisterBlobServiceServer(grpcServer, blob.NewServer())
	pb.RegisterCommitServiceServer(grpcServer, commit.NewServer(rubyServer))
	pb.RegisterDiffServiceServer(grpcServer, diff.NewServer(rubyServer))
	pb.RegisterNamespaceServiceServer(grpcServer, namespace.NewServer())
	pb.RegisterNotificationServiceServer(grpcServer, notifications.NewServer())
	pb.RegisterOperationServiceServer(grpcServer, operations.NewServer(rubyServer))
	pb.RegisterRefServiceServer(grpcServer, ref.NewServer(rubyServer))
	pb.RegisterRepositoryServiceServer(grpcServer, repository.NewServer(rubyServer))
	pb.RegisterSSHServiceServer(grpcServer, ssh.NewServer())
	pb.RegisterSmartHTTPServiceServer(grpcServer, smarthttp.NewServer())

	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
}
