package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

func (*server) RestoreCustomHooks(pb.RepositoryService_RestoreCustomHooksServer) error {
	return helper.Unimplemented
}
