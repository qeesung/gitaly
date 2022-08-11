package hook

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
)

//nolint: stylecheck // This is unintentionally missing documentation.
func (m *GitLabHookManager) Check(ctx context.Context) (*gitlab.CheckInfo, error) {
	return m.gitlabClient.Check(ctx)
}
