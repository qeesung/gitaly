//go:build !gitaly_test_sha256

package praefectutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestDeriveReplicaPath(t *testing.T) {
	require.Equal(t, "@cluster/repositories/6b/86/1", DeriveReplicaPath(1))
	require.Equal(t, "@cluster/repositories/d4/73/2", DeriveReplicaPath(2))
}

func TestDerivePoolPath(t *testing.T) {
	require.Equal(t, "@cluster/pools/6b/86/1", DerivePoolPath(1))
	require.Equal(t, "@cluster/pools/d4/73/2", DerivePoolPath(2))
}

func TestIsPoolRepository(t *testing.T) {
	for _, tc := range []struct {
		desc       string
		repo       *gitalypb.Repository
		isPoolPath bool
	}{
		{
			desc:       "missing repository",
			isPoolPath: false,
		},
		{
			desc: "empty string",
			repo: &gitalypb.Repository{
				RelativePath: "",
			},
			isPoolPath: false,
		},
		{
			desc: "praefect pool path",
			repo: &gitalypb.Repository{
				RelativePath: DerivePoolPath(1),
			},
			isPoolPath: true,
		},
		{
			desc: "praefect replica path",
			repo: &gitalypb.Repository{
				RelativePath: DeriveReplicaPath(1),
			},
		},
		{
			desc: "rails pool path",
			repo: &gitalypb.Repository{
				RelativePath: gittest.NewObjectPoolName(t),
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.isPoolPath, IsPoolRepository(tc.repo))
		})
	}
}
