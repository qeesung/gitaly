package repository

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestGetFileAttributes(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	gitattributesContent := "*.go diff=go text\n*.md text\n*.jpg -text"
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("main"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: ".gitattributes", Mode: "100644", Content: gitattributesContent},
			gittest.TreeEntry{Path: "example.go", Mode: "100644", Content: "something important"},
			gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "some text"},
			gittest.TreeEntry{Path: "pic.jpg", Mode: "100644", Content: "blob"},
		))

	type setupData struct {
		request          *gitalypb.GetFileAttributesRequest
		expectedResponse *gitalypb.GetFileAttributesResponse
		expectedErr      error
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "returns a single file attributes successfully",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: []string{"diff"},
						Paths:      []string{"example.go"},
					},
					expectedResponse: &gitalypb.GetFileAttributesResponse{
						AttributeInfos: []*gitalypb.GetFileAttributesResponse_AttributeInfo{
							{
								Path:      string("example.go"),
								Attribute: string("diff"),
								Value:     string("go"),
							},
						},
					},
				}
			},
		},
		{
			desc: "returns multiple file attributes successfully",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: []string{"diff", "text"},
						Paths:      []string{"example.go", "README.md", "pic.jpg"},
					},
					expectedResponse: &gitalypb.GetFileAttributesResponse{
						AttributeInfos: []*gitalypb.GetFileAttributesResponse_AttributeInfo{
							{
								Path:      string("example.go"),
								Attribute: string("diff"),
								Value:     string("go"),
							},
							{
								Path:      string("example.go"),
								Attribute: string("text"),
								Value:     string("set"),
							},
							{
								Path:      string("README.md"),
								Attribute: string("text"),
								Value:     string("set"),
							},
							{
								Path:      string("pic.jpg"),
								Attribute: string("text"),
								Value:     string("unset"),
							},
						},
					},
				}
			},
		},
		{
			desc: "returns multiple attributes for a file successfully",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: []string{"*"},
						Paths:      []string{"example.go"},
					},
					expectedResponse: &gitalypb.GetFileAttributesResponse{
						AttributeInfos: []*gitalypb.GetFileAttributesResponse_AttributeInfo{
							{
								Path:      string("example.go"),
								Attribute: string("diff"),
								Value:     string("go"),
							},
							{
								Path:      string("example.go"),
								Attribute: string("text"),
								Value:     string("set"),
							},
						},
					},
				}
			},
		},
		{
			desc: "returns empty response when there are no matching attributes for the given path",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: []string{"diff"},
						Paths:      []string{"example.txt"},
					},
					expectedResponse: &gitalypb.GetFileAttributesResponse{
						AttributeInfos: []*gitalypb.GetFileAttributesResponse_AttributeInfo{},
					},
				}
			},
		},
		{
			desc: "returns an error when no revision is given",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   nil,
						Attributes: []string{"diff"},
						Paths:      []string{"example.go"},
					},
					expectedErr: structerr.NewInvalidArgument("revision is required"),
				}
			},
		},
		{
			desc: "returns an error when no path is given",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: []string{"diff"},
						Paths:      nil,
					},
					expectedErr: structerr.NewInvalidArgument("file paths are required"),
				}
			},
		},
		{
			desc: "returns an error when no attribute is given",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.GetFileAttributesRequest{
						Repository: repoProto,
						Revision:   []byte("main"),
						Attributes: nil,
						Paths:      []string{"example.go"},
					},
					expectedErr: structerr.NewInvalidArgument("attributes are required"),
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			setupData := tc.setup(t)
			response, err := client.GetFileAttributes(ctx, setupData.request)

			testhelper.RequireGrpcError(t, setupData.expectedErr, err)
			testhelper.ProtoEqual(t, setupData.expectedResponse, response)
		})
	}
}
