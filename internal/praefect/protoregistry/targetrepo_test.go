package protoregistry_test

import (
	"errors"
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
		svc        string
		method     string
		pbMsg      proto.Message
		expectRepo *gitalypb.Repository
		expectErr  error
	}{
		{
			svc:    "RepositoryService",
			method: "RepackIncremental",
			pbMsg: &gitalypb.RepackIncrementalRequest{
				Repository: testRepos[0],
			},
			expectRepo: testRepos[0],
		},
		{
			svc:        "RepositoryService",
			method:     "RepackIncremental",
			pbMsg:      &gitalypb.RepackIncrementalResponse{},
			expectRepo: nil,
			expectErr:  errors.New("proto message gitaly.RepackIncrementalResponse does not match expected RPC request message gitaly.RepackIncrementalRequest"),
		},
	}

	for _, tc := range testcases {
		desc := fmt.Sprintf("%s:%s", tc.svc, tc.method)
		t.Run(desc, func(t *testing.T) {
			info, err := r.LookupMethod(tc.svc, tc.method)
			require.NoError(t, err)

			actualTarget, actualErr := info.TargetRepo(tc.pbMsg)
			require.Equal(t, tc.expectErr, actualErr)
			if tc.expectRepo != actualTarget {
				t.Fatal("pointers do not match")
			}
		})
	}
}
