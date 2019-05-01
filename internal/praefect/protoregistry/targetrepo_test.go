package protoregistry_test

import (
	"fmt"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
)

func TestTargetRepo(t *testing.T) {
	r := protoregistry.New()
	require.NoError(t, r.RegisterFiles(protoregistry.GitalyProtoFileDescriptors...))

	testRepos := []*gitalypb.Repository{
		&gitalypb.Repository{
			GitAlternateObjectDirectories: []string{"a", "b", "c"},
			GitObjectDirectory:            "d",
			GlProjectPath:                 "e",
			GlRepository:                  "f",
			RelativePath:                  "g",
			StorageName:                   "h",
		},
		&gitalypb.Repository{
			GitAlternateObjectDirectories: []string{"1", "2", "3"},
			GitObjectDirectory:            "4",
			GlProjectPath:                 "5",
			GlRepository:                  "6",
			RelativePath:                  "7",
			StorageName:                   "8",
		},
	}

	testcases := []struct {
		svc    string
		method string
		proto.Message
		*gitalypb.Repository
	}{
		{
			svc:    "RepositoryService",
			method: "RepackIncremental",
			Message: &gitalypb.RepackIncrementalRequest{
				Repository: testRepos[0],
			},
			Repository: testRepos[0],
		},
	}

	for _, tc := range testcases {
		desc := fmt.Sprintf("%s:%s", tc.svc, tc.method)
		t.Run(desc, func(t *testing.T) {
			info, err := r.LookupMethod(tc.svc, tc.method)
			require.NoError(t, err)

			actualTarget, err := info.TargetRepo(tc.Message)
			require.NoError(t, err)
			require.Equal(t, testRepos[0], actualTarget)
			if testRepos[0] != actualTarget {
				t.Fatalf("pointers do not match: %p vs %p", testRepos[0], actualTarget)
			}
		})
	}
}
