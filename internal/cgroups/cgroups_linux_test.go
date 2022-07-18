//go:build !gitaly_test_sha256

package cgroups

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestNewManager(t *testing.T) {
	cfg := cgroups.Config{Repositories: cgroups.Repositories{Count: 10}}

	require.IsType(t, &CGroupV1Manager{}, &CGroupV1Manager{cfg: cfg})
	require.IsType(t, &NoopManager{}, NewManager(cgroups.Config{}))
}
