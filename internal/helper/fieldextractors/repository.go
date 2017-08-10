package fieldextractors

import (
	"strings"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func formatRepoRequest(repo *pb.Repository) map[string]interface{} {
	if repo == nil {
		// Signals that the client did not send a repo through, which
		// will be useful for logging
		return map[string]interface{}{
			"repo": nil,
		}
	}

	return map[string]interface{}{
		"repoStorage":   repo.StorageName,
		"repoPath":      repo.RelativePath,
		"topLevelGroup": getTopLevelGroupFromRepoPath(repo.RelativePath),
	}
}

// getTopLevelGroupFromRepoPath gives the top-level group name, given
// a repoPath. For example:
// - "gitlab-org/gitlab-ce.git" returns "gitlab-org"
// - "gitlab-org/gitter/webapp.git" returns "gitlab-org"
// - "x.git" returns ""
func getTopLevelGroupFromRepoPath(repoPath string) string {
	parts := strings.SplitN(repoPath, "/", 2)
	if len(parts) != 2 {
		return ""
	}

	return parts[0]
}

type repositoryBasedRequest interface {
	GetRepository() *pb.Repository
}

// RepositoryFieldExtractor will extract the repository fields from an incoming grpc request
func RepositoryFieldExtractor(fullMethod string, req interface{}) map[string]interface{} {
	if req == nil {
		return nil
	}

	if repoReq, ok := req.(repositoryBasedRequest); ok {
		return formatRepoRequest(repoReq.GetRepository())
	}

	// Add other request handlers here in future
	return nil

}
