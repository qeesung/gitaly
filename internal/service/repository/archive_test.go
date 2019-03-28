package repository

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"
)

func TestGetArchiveSuccess(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	formats := []gitalypb.GetArchiveRequest_Format{
		gitalypb.GetArchiveRequest_ZIP,
		gitalypb.GetArchiveRequest_TAR,
		gitalypb.GetArchiveRequest_TAR_GZ,
		gitalypb.GetArchiveRequest_TAR_BZ2,
	}

	testCases := []struct {
		desc     string
		prefix   string
		commitID string
		path     []byte
		contents []string
	}{
		{
			desc:     "without-prefix",
			commitID: "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			prefix:   "",
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with-prefix",
			commitID: "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			prefix:   "my-prefix",
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path as blank string",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte(""),
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path as nil",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     nil,
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte("files"),
			contents: []string{"/whitespace", "/html/500.html"},
		},
		{
			desc:     "with path and trailing slash",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte("files/"),
			contents: []string{"/whitespace", "/html/500.html"},
		},
	}

	for _, tc := range testCases {
		// Run test case with each format
		for _, format := range formats {
			testCaseName := fmt.Sprintf("%s-%s", tc.desc, format.String())
			t.Run(testCaseName, func(t *testing.T) {
				ctx, cancel := testhelper.Context()
				defer cancel()

				req := &gitalypb.GetArchiveRequest{
					Repository: testRepo,
					CommitId:   tc.commitID,
					Prefix:     tc.prefix,
					Format:     format,
					Path:       tc.path,
				}
				stream, err := client.GetArchive(ctx, req)
				require.NoError(t, err)

				data, err := consumeArchive(stream)
				require.NoError(t, err)

				archiveFile, err := ioutil.TempFile("", "")
				require.NoError(t, err)
				defer os.Remove(archiveFile.Name())

				_, err = archiveFile.Write(data)
				require.NoError(t, err)

				contents := string(compressedFileContents(t, format, archiveFile.Name()))

				for _, content := range tc.contents {
					require.Contains(t, contents, tc.prefix+content)
				}
			})
		}
	}
}

func TestGetArchiveFailure(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	commitID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	testCases := []struct {
		desc     string
		repo     *gitalypb.Repository
		prefix   string
		commitID string
		format   gitalypb.GetArchiveRequest_Format
		path     []byte
		code     codes.Code
	}{
		{
			desc:     "Repository doesn't exist",
			repo:     &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Repository is nil",
			repo:     nil,
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "CommitId is empty",
			repo:     testRepo,
			prefix:   "",
			commitID: "",
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Format is invalid",
			repo:     testRepo,
			prefix:   "",
			commitID: "",
			format:   gitalypb.GetArchiveRequest_Format(-1),
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Non-existing path in repository",
			repo:     testRepo,
			prefix:   "",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("unknown-path"),
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "Non-existing path in repository on commit ID",
			repo:     testRepo,
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("files/"),
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "path contains directory traversal",
			repo:     testRepo,
			prefix:   "",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("../../foo"),
			code:     codes.InvalidArgument,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			req := &gitalypb.GetArchiveRequest{
				Repository: tc.repo,
				CommitId:   tc.commitID,
				Prefix:     tc.prefix,
				Format:     tc.format,
				Path:       tc.path,
			}
			stream, err := client.GetArchive(ctx, req)
			require.NoError(t, err)

			_, err = consumeArchive(stream)
			testhelper.RequireGrpcError(t, err, tc.code)
		})
	}
}

func compressedFileContents(t *testing.T, format gitalypb.GetArchiveRequest_Format, name string) []byte {
	switch format {
	case gitalypb.GetArchiveRequest_TAR:
		return testhelper.MustRunCommand(t, nil, "tar", "tf", name)
	case gitalypb.GetArchiveRequest_TAR_GZ:
		return testhelper.MustRunCommand(t, nil, "tar", "ztf", name)
	case gitalypb.GetArchiveRequest_TAR_BZ2:
		return testhelper.MustRunCommand(t, nil, "tar", "jtf", name)
	case gitalypb.GetArchiveRequest_ZIP:
		return testhelper.MustRunCommand(t, nil, "unzip", "-l", name)
	}

	return nil
}

func consumeArchive(stream gitalypb.RepositoryService_GetArchiveClient) ([]byte, error) {
	reader := streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	})

	return ioutil.ReadAll(reader)
}
