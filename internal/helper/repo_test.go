package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestRepoPathEqual(t *testing.T) {
	testCases := []struct {
		desc  string
		a, b  *gitalypb.Repository
		equal bool
	}{
		{
			desc: "equal",
			a: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "repo.git",
			},
			b: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "repo.git",
			},
			equal: true,
		},
		{
			desc: "different storage",
			a: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "repo.git",
			},
			b: &gitalypb.Repository{
				StorageName:  "storage2",
				RelativePath: "repo.git",
			},
			equal: false,
		},
		{
			desc: "different path",
			a: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "repo.git",
			},
			b: &gitalypb.Repository{
				StorageName:  "default",
				RelativePath: "repo2.git",
			},
			equal: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			assert.Equal(t, tc.equal, RepoPathEqual(tc.a, tc.b))
		})
	}
}
