package protoregistry_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestProtoRegistryTargetRepo(t *testing.T) {
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
		desc                 string
		svc                  string
		method               string
		pbMsg                proto.Message
		expectRepo           *gitalypb.Repository
		expectAdditionalRepo *gitalypb.Repository
		expectErr            error
	}{
		{
			desc:   "valid request type single depth",
			svc:    "RepositoryService",
			method: "RepackIncremental",
			pbMsg: &gitalypb.RepackIncrementalRequest{
				Repository: testRepos[0],
			},
			expectRepo: testRepos[0],
		},
		{
			desc:      "incorrect request type",
			svc:       "RepositoryService",
			method:    "RepackIncremental",
			pbMsg:     &gitalypb.RepackIncrementalResponse{},
			expectErr: errors.New("proto message gitaly.RepackIncrementalResponse does not match expected RPC request message gitaly.RepackIncrementalRequest"),
		},
		{
			desc:   "target nested in oneOf",
			svc:    "OperationService",
			method: "UserCommitFiles",
			pbMsg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
					Header: &gitalypb.UserCommitFilesRequestHeader{
						Repository: testRepos[1],
					},
				},
			},
			expectRepo: testRepos[1],
		},
		{
			desc:   "target nested, includes additional repository",
			svc:    "ObjectPoolService",
			method: "FetchIntoObjectPool",
			pbMsg: &gitalypb.FetchIntoObjectPoolRequest{
				Origin:     testRepos[0],
				ObjectPool: &gitalypb.ObjectPool{Repository: testRepos[1]},
				Repack:     false,
			},
			expectRepo:           testRepos[1],
			expectAdditionalRepo: testRepos[0],
		},
		{
			desc:      "target repo is nil",
			svc:       "RepositoryService",
			method:    "RepackIncremental",
			pbMsg:     &gitalypb.RepackIncrementalRequest{Repository: nil},
			expectErr: protoregistry.ErrTargetRepoMissing,
		},
	}

	for _, tc := range testcases {
		desc := fmt.Sprintf("%s:%s %s", tc.svc, tc.method, tc.desc)
		t.Run(desc, func(t *testing.T) {
			info, err := r.LookupMethod(fmt.Sprintf("/gitaly.%s/%s", tc.svc, tc.method))
			require.NoError(t, err)

			actualTarget, actualErr := info.TargetRepo(tc.pbMsg)
			require.Equal(t, tc.expectErr, actualErr)

			// not only do we want the value to be the same, but we actually want the
			// exact same instance to be returned
			if tc.expectRepo != actualTarget {
				t.Fatal("pointers do not match")
			}

			if tc.expectAdditionalRepo != nil {
				additionalRepo, ok, err := info.AdditionalRepo(tc.pbMsg)
				require.True(t, ok)
				require.NoError(t, err)
				require.Equal(t, tc.expectAdditionalRepo, additionalRepo)
			}
		})
	}
}

func TestProtoRegistryStorage(t *testing.T) {
	r := protoregistry.New()
	require.NoError(t, r.RegisterFiles(protoregistry.GitalyProtoFileDescriptors...))

	testcases := []struct {
		desc          string
		svc           string
		method        string
		pbMsg         proto.Message
		expectStorage string
		expectErr     error
	}{
		{
			desc:   "valid request type single depth",
			svc:    "NamespaceService",
			method: "AddNamespace",
			pbMsg: &gitalypb.AddNamespaceRequest{
				StorageName: "some_storage",
			},
			expectStorage: "some_storage",
		},
		{
			desc:      "incorrect request type",
			svc:       "RepositoryService",
			method:    "RepackIncremental",
			pbMsg:     &gitalypb.RepackIncrementalResponse{},
			expectErr: errors.New("proto message gitaly.RepackIncrementalResponse does not match expected RPC request message gitaly.RepackIncrementalRequest"),
		},
	}

	for _, tc := range testcases {
		desc := fmt.Sprintf("%s:%s %s", tc.svc, tc.method, tc.desc)
		t.Run(desc, func(t *testing.T) {
			info, err := r.LookupMethod(fmt.Sprintf("/gitaly.%s/%s", tc.svc, tc.method))
			require.NoError(t, err)

			actualStorage, actualErr := info.Storage(tc.pbMsg)
			require.Equal(t, tc.expectErr, actualErr)

			// not only do we want the value to be the same, but we actually want the
			// exact same instance to be returned
			if tc.expectStorage != actualStorage {
				t.Fatal("pointers do not match")
			}
		})
	}
}
