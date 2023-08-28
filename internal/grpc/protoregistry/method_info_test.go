package protoregistry

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestMethodInfo_getRepo(t *testing.T) {
	t.Parallel()

	testRepos := []*gitalypb.Repository{
		{
			GitAlternateObjectDirectories: []string{"a", "b", "c"},
			GitObjectDirectory:            "d",
			GlProjectPath:                 "e",
			GlRepository:                  "f",
			RelativePath:                  "g",
			StorageName:                   "h",
		},
		{
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
		expectErr            error
		expectAdditionalRepo *gitalypb.Repository
		expectAdditionalErr  error
	}{
		{
			desc:   "valid request type single depth",
			svc:    "RepositoryService",
			method: "OptimizeRepository",
			pbMsg: &gitalypb.OptimizeRepositoryRequest{
				Repository: testRepos[0],
			},
			expectRepo:          testRepos[0],
			expectAdditionalErr: ErrRepositoryFieldNotFound,
		},
		{
			desc:                "unset oneof",
			svc:                 "OperationService",
			method:              "UserCommitFiles",
			pbMsg:               &gitalypb.UserCommitFilesRequest{},
			expectErr:           ErrRepositoryFieldNotFound,
			expectAdditionalErr: ErrRepositoryFieldNotFound,
		},
		{
			desc:   "unset value in oneof",
			svc:    "OperationService",
			method: "UserCommitFiles",
			pbMsg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{},
			},
			expectErr:           ErrRepositoryFieldNotFound,
			expectAdditionalErr: ErrRepositoryFieldNotFound,
		},
		{
			desc:   "unset repository in oneof",
			svc:    "OperationService",
			method: "UserCommitFiles",
			pbMsg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
					Header: &gitalypb.UserCommitFilesRequestHeader{},
				},
			},
			expectErr:           ErrRepositoryFieldNotFound,
			expectAdditionalErr: ErrRepositoryFieldNotFound,
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
			expectRepo:          testRepos[1],
			expectAdditionalErr: ErrRepositoryFieldNotFound,
		},
		{
			desc:   "target nested, includes additional repository",
			svc:    "ObjectPoolService",
			method: "FetchIntoObjectPool",
			pbMsg: &gitalypb.FetchIntoObjectPoolRequest{
				Origin:     testRepos[0],
				ObjectPool: &gitalypb.ObjectPool{Repository: testRepos[1]},
			},
			expectRepo:           testRepos[1],
			expectAdditionalRepo: testRepos[0],
		},
		{
			desc:                "target repo is nil",
			svc:                 "RepositoryService",
			method:              "OptimizeRepository",
			pbMsg:               &gitalypb.OptimizeRepositoryRequest{Repository: nil},
			expectErr:           ErrRepositoryFieldNotFound,
			expectAdditionalErr: ErrRepositoryFieldNotFound,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			info, err := GitalyProtoPreregistered.LookupMethod(fmt.Sprintf("/gitaly.%s/%s", tc.svc, tc.method))
			require.NoError(t, err)

			t.Run("TargetRepo", func(t *testing.T) {
				repo, err := info.TargetRepo(tc.pbMsg)
				require.Equal(t, tc.expectErr, err)
				require.Same(t, tc.expectRepo, repo)
			})

			t.Run("AdditionalRepo", func(t *testing.T) {
				additionalRepo, err := info.AdditionalRepo(tc.pbMsg)
				require.Equal(t, tc.expectAdditionalErr, err)
				require.Same(t, tc.expectAdditionalRepo, additionalRepo)
			})
		})
	}
}

func TestMethodInfo_NewRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc            string
		fullMethod      string
		expectedRequest proto.Message
	}{
		{
			fullMethod:      "/gitaly.BlobService/GetBlobs",
			expectedRequest: &gitalypb.GetBlobsRequest{},
		},
		{
			fullMethod:      "/gitaly.RepositoryService/CreateRepository",
			expectedRequest: &gitalypb.CreateRepositoryRequest{},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			methodInfo, err := GitalyProtoPreregistered.LookupMethod(tc.fullMethod)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, tc.expectedRequest, methodInfo.NewRequest())
		})
	}
}

func TestMethodInfo_Storage(t *testing.T) {
	t.Parallel()

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
			svc:    "InternalGitaly",
			method: "WalkRepos",
			pbMsg: &gitalypb.WalkReposRequest{
				StorageName: "some_storage",
			},
			expectStorage: "some_storage",
		},
		{
			desc:      "incorrect request type",
			svc:       "RepositoryService",
			method:    "OptimizeRepository",
			pbMsg:     &gitalypb.OptimizeRepositoryResponse{},
			expectErr: errors.New("proto message gitaly.OptimizeRepositoryResponse does not match expected RPC request message gitaly.OptimizeRepositoryRequest"),
		},
	}

	for _, tc := range testcases {
		desc := fmt.Sprintf("%s:%s %s", tc.svc, tc.method, tc.desc)
		t.Run(desc, func(t *testing.T) {
			info, err := GitalyProtoPreregistered.LookupMethod(fmt.Sprintf("/gitaly.%s/%s", tc.svc, tc.method))
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

func TestMethodInfo_SetStorage(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc      string
		service   string
		method    string
		pbMsg     proto.Message
		storage   string
		expectErr error
	}{
		{
			desc:    "valid request type",
			service: "InternalGitaly",
			method:  "WalkRepos",
			pbMsg: &gitalypb.WalkReposRequest{
				StorageName: "old_storage",
			},
			storage: "new_storage",
		},
		{
			desc:      "incorrect request type",
			service:   "RepositoryService",
			method:    "OptimizeRepository",
			pbMsg:     &gitalypb.OptimizeRepositoryResponse{},
			expectErr: errors.New("proto message gitaly.OptimizeRepositoryResponse does not match expected RPC request message gitaly.OptimizeRepositoryRequest"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			info, err := GitalyProtoPreregistered.LookupMethod("/gitaly." + tc.service + "/" + tc.method)
			require.NoError(t, err)

			err = info.SetStorage(tc.pbMsg, tc.storage)
			if tc.expectErr == nil {
				require.NoError(t, err)
				changed, err := info.Storage(tc.pbMsg)
				require.NoError(t, err)
				require.Equal(t, tc.storage, changed)
			} else {
				require.Equal(t, tc.expectErr, err)
			}
		})
	}
}

func TestMethodInfo_RequestFactory(t *testing.T) {
	t.Parallel()

	mInfo, err := GitalyProtoPreregistered.LookupMethod("/gitaly.RepositoryService/RepositoryExists")
	require.NoError(t, err)

	pb, err := mInfo.UnmarshalRequestProto([]byte{})
	require.NoError(t, err)

	testhelper.ProtoEqual(t, &gitalypb.RepositoryExistsRequest{}, pb)
}

func TestMethodInfoScope(t *testing.T) {
	for _, tt := range []struct {
		method string
		scope  Scope
	}{
		{
			method: "/gitaly.RepositoryService/RepositoryExists",
			scope:  ScopeRepository,
		},
	} {
		t.Run(tt.method, func(t *testing.T) {
			mInfo, err := GitalyProtoPreregistered.LookupMethod(tt.method)
			require.NoError(t, err)

			require.Exactly(t, tt.scope, mInfo.Scope)
		})
	}
}

func TestFindFieldsByExtension(t *testing.T) {
	t.Parallel()

	repo := &gitalypb.Repository{StorageName: "storage", RelativePath: "relative-path"}

	for _, tc := range []struct {
		desc           string
		msg            proto.Message
		extension      protoreflect.ExtensionType
		expectedFields []string
	}{
		{
			desc: "unset field does not match",
			msg: &gitalypb.OptimizeRepositoryRequest{
				Repository: nil,
			},
			extension: gitalypb.E_TargetRepository,
		},
		{
			desc: "matching field",
			msg: &gitalypb.OptimizeRepositoryRequest{
				Repository: repo,
			},
			extension: gitalypb.E_TargetRepository,
			expectedFields: []string{
				"gitaly.OptimizeRepositoryRequest.repository",
			},
		},
		{
			desc: "no matching field",
			msg: &gitalypb.OptimizeRepositoryRequest{
				Repository: repo,
			},
			extension: gitalypb.E_AdditionalRepository,
		},
		{
			desc: "multiple fields with distinct extensions",
			msg: &gitalypb.FetchIntoObjectPoolRequest{
				Origin:     repo,
				ObjectPool: &gitalypb.ObjectPool{Repository: repo},
			},
			extension: gitalypb.E_AdditionalRepository,
			expectedFields: []string{
				"gitaly.FetchIntoObjectPoolRequest.origin",
			},
		},
		{
			desc:      "matching field in unset oneOf",
			msg:       &gitalypb.UserCommitFilesRequest{},
			extension: gitalypb.E_TargetRepository,
		},
		{
			desc: "matching field in empty oneOf",
			msg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{},
			},
			extension: gitalypb.E_TargetRepository,
		},
		{
			desc: "matching field with unset oneOf repository",
			msg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
					Header: &gitalypb.UserCommitFilesRequestHeader{},
				},
			},
			extension: gitalypb.E_TargetRepository,
		},
		{
			desc: "matching field in oneOf",
			msg: &gitalypb.UserCommitFilesRequest{
				UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
					Header: &gitalypb.UserCommitFilesRequestHeader{
						Repository: repo,
					},
				},
			},
			extension: gitalypb.E_TargetRepository,
			expectedFields: []string{
				"gitaly.UserCommitFilesRequestHeader.repository",
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			t.Run("findFieldsByExtension", func(t *testing.T) {
				fields, err := findFieldsByExtension(tc.msg, tc.extension)
				require.NoError(t, err)

				var fieldNames []string
				for _, field := range fields {
					fieldNames = append(fieldNames, string(field.desc.FullName()))
				}
				require.Equal(t, tc.expectedFields, fieldNames)
			})

			t.Run("findFieldByExtension", func(t *testing.T) {
				field, err := findFieldByExtension(tc.msg, tc.extension)

				switch len(tc.expectedFields) {
				case 0:
					require.Equal(t, valueField{}, field)
					require.Equal(t, errFieldNotFound, err)
				case 1:
					require.NoError(t, err)
					require.Equal(t, tc.expectedFields[0], string(field.desc.FullName()))
				default:
					require.Equal(t, valueField{}, field)
					require.Equal(t, errFieldAmbiguous, err)
				}
			})
		})
	}
}

func BenchmarkMethodInfo(b *testing.B) {
	for _, bc := range []struct {
		desc        string
		method      string
		request     proto.Message
		expectedErr error
	}{
		{
			desc:   "unset target repository",
			method: "/gitaly.RepositoryService/OptimizeRepository",
			request: &gitalypb.OptimizeRepositoryRequest{
				Repository: nil,
			},
			expectedErr: ErrRepositoryFieldNotFound,
		},
		{
			desc:   "target repository",
			method: "/gitaly.RepositoryService/OptimizeRepository",
			request: &gitalypb.OptimizeRepositoryRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "something",
					RelativePath: "something",
				},
			},
		},
		{
			desc:   "target object pool",
			method: "/gitaly.ObjectPoolService/FetchIntoObjectPool",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  "something",
						RelativePath: "something",
					},
				},
			},
		},
	} {
		b.Run(bc.desc, func(b *testing.B) {
			mi, err := GitalyProtoPreregistered.LookupMethod(bc.method)
			require.NoError(b, err)

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := mi.TargetRepo(bc.request)
				require.Equal(b, bc.expectedErr, err)
			}
		})
	}
}
