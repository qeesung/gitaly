package service

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/service/commit"
	"gitlab.com/gitlab-org/gitaly/internal/service/diff"
	"gitlab.com/gitlab-org/gitaly/internal/service/notifications"
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/service/renameadapter"
	"gitlab.com/gitlab-org/gitaly/internal/service/smarthttp"
	"gitlab.com/gitlab-org/gitaly/internal/service/ssh"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterAll will register all the known grpc services with
// the specified grpc service instance
func RegisterAll(grpcServer *grpc.Server) {
	notificationsService := notifications.NewServer()
	pb.RegisterNotificationServiceServer(grpcServer, notificationsService)

	refService := ref.NewServer()
	pb.RegisterRefServiceServer(grpcServer, refService)

	smartHTTPService := smarthttp.NewServer()
	pb.RegisterSmartHTTPServiceServer(grpcServer, smartHTTPService)

	diffService := diff.NewServer()
	pb.RegisterDiffServiceServer(grpcServer, diffService)

	commitService := commit.NewServer()
	pb.RegisterCommitServiceServer(grpcServer, commitService)

	sshService := ssh.NewServer()
	pb.RegisterSSHServiceServer(grpcServer, sshService)

	// Deprecated Services
	pb.RegisterNotificationsServer(grpcServer, renameadapter.NewNotificationAdapter(notificationsService))
	pb.RegisterRefServer(grpcServer, renameadapter.NewRefAdapter(refService))
	pb.RegisterSmartHTTPServer(grpcServer, renameadapter.NewSmartHTTPAdapter(smartHTTPService))
	pb.RegisterDiffServer(grpcServer, renameadapter.NewDiffAdapter(diffService))
	pb.RegisterCommitServer(grpcServer, renameadapter.NewCommitAdapter(commitService))
	pb.RegisterSSHServer(grpcServer, renameadapter.NewSSHAdapter(sshService))

	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
}
