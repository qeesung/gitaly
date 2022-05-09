package setup

import (
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/blob"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/cleanup"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/commit"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/conflicts"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/diff"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/internalgitaly"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/namespace"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/objectpool"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/operations"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/remote"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/server"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/smarthttp"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/ssh"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/wiki"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

var (
	smarthttpPackfileNegotiationMetrics = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "smarthttp",
			Name:      "packfile_negotiation_requests_total",
			Help:      "Total number of features used for packfile negotiations",
		},
		[]string{"git_negotiation_feature"},
	)

	sshPackfileNegotiationMetrics = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "gitaly",
			Subsystem: "ssh",
			Name:      "packfile_negotiation_requests_total",
			Help:      "Total number of features used for packfile negotiations",
		},
		[]string{"git_negotiation_feature"},
	)
)

// RegisterAll will register all the known gRPC services on  the provided gRPC service instance.
func RegisterAll(srv *grpc.Server, deps *service.Dependencies) {
	gitalypb.RegisterBlobServiceServer(srv, blob.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
	))
	gitalypb.RegisterCleanupServiceServer(srv, cleanup.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
	))
	gitalypb.RegisterCommitServiceServer(srv, commit.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetLinguist(),
		deps.GetCatfileCache(),
	))
	gitalypb.RegisterDiffServiceServer(srv, diff.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
	))
	gitalypb.RegisterNamespaceServiceServer(srv, namespace.NewServer(deps.GetLocator()))
	gitalypb.RegisterOperationServiceServer(srv, operations.NewServer(
		deps.GetHookManager(),
		deps.GetTxManager(),
		deps.GetLocator(),
		deps.GetConnsPool(),
		deps.GetGit2goExecutor(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
		deps.GetUpdaterWithHooks(),
	))
	gitalypb.RegisterRefServiceServer(srv, ref.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetTxManager(),
		deps.GetCatfileCache(),
	))
	gitalypb.RegisterRepositoryServiceServer(srv, repository.NewServer(
		deps.GetCfg(),
		deps.GetRubyServer(),
		deps.GetLocator(),
		deps.GetTxManager(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
		deps.GetConnsPool(),
		deps.GetGit2goExecutor(),
		deps.GetHousekeepingManager(),
	))
	gitalypb.RegisterSSHServiceServer(srv, ssh.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetTxManager(),
		ssh.WithPackfileNegotiationMetrics(sshPackfileNegotiationMetrics),
	))
	gitalypb.RegisterSmartHTTPServiceServer(srv, smarthttp.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetTxManager(),
		deps.GetDiskCache(),
		smarthttp.WithPackfileNegotiationMetrics(smarthttpPackfileNegotiationMetrics),
	))
	gitalypb.RegisterWikiServiceServer(srv, wiki.NewServer(deps.GetRubyServer(), deps.GetLocator()))
	gitalypb.RegisterConflictsServiceServer(srv, conflicts.NewServer(
		deps.GetHookManager(),
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
		deps.GetConnsPool(),
		deps.GetGit2goExecutor(),
		deps.GetUpdaterWithHooks(),
	))
	gitalypb.RegisterRemoteServiceServer(srv, remote.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
		deps.GetTxManager(),
		deps.GetConnsPool(),
	))
	gitalypb.RegisterServerServiceServer(srv, server.NewServer(deps.GetGitCmdFactory(), deps.GetCfg().Storages))
	gitalypb.RegisterObjectPoolServiceServer(srv, objectpool.NewServer(
		deps.GetLocator(),
		deps.GetGitCmdFactory(),
		deps.GetCatfileCache(),
		deps.GetTxManager(),
		deps.GetHousekeepingManager(),
	))
	gitalypb.RegisterHookServiceServer(srv, hook.NewServer(
		deps.GetHookManager(),
		deps.GetGitCmdFactory(),
		deps.GetPackObjectsCache(),
	))
	gitalypb.RegisterInternalGitalyServer(srv, internalgitaly.NewServer(deps.GetCfg().Storages))

	healthpb.RegisterHealthServer(srv, health.NewServer())
	reflection.Register(srv)
	grpcprometheus.Register(srv)
}
