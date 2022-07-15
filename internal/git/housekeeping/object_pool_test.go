package housekeeping

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/praefectutil"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestIsPoolRepository(t *testing.T) {
	for _, tc := range []struct {
		desc       string
		repo       *gitalypb.Repository
		isPoolPath bool
	}{
		{
			desc: "rails pool directory",
			repo: &gitalypb.Repository{
				RelativePath: gittest.NewObjectPoolName(t),
			},
			isPoolPath: true,
		},
		{
			desc: "praefect pool path",
			repo: &gitalypb.Repository{
				RelativePath: praefectutil.DerivePoolPath(1),
			},
			isPoolPath: true,
		},
		{
			desc: "praefect replica path",
			repo: &gitalypb.Repository{
				RelativePath: praefectutil.DeriveReplicaPath(1),
			},
		},
		{
			desc: "missing repository",
		},
		{
			desc: "empty repository",
			repo: &gitalypb.Repository{},
		},
		{
			desc: "rails path first to subdirs dont match full hash",
			repo: &gitalypb.Repository{
				RelativePath: "@pools/aa/bb/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff.git",
			},
		},
		{
			desc: "normal repos dont match",
			repo: &gitalypb.Repository{
				RelativePath: "@hashed/" + gittest.NewRepositoryName(t, true),
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.isPoolPath, IsPoolRepository(tc.repo))
		})
	}
}
