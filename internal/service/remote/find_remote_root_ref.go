package remote

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	log "github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"golang.org/x/net/context"
)

// FindRemoteRootRef queries the remote to determine its HEAD
func (s *server) FindRemoteRootRef(ctx context.Context, in *gitalypb.FindRemoteRootRefRequest) (*gitalypb.FindRemoteRootRefResponse, error) {
	grpc_logrus.Extract(ctx).WithFields(log.Fields{
		"Remote":     in.GetRemote(),
		"SSHKey":     in.GetCredentials().GetSshKey(),
		"KnownHosts": in.GetCredentials().GetKnownHosts(),
	}).Debug("FindRemoteRootRef")

	client, err := s.RemoteServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, in.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.FindRemoteRootRef(clientCtx, in)
}
