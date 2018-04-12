package wiki

import (
	"bytes"
	"testing"

	gitlog "gitlab.com/gitlab-org/gitaly/internal/git/log"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

func TestSuccessfulWikiUpdatePageRequest(t *testing.T) {
	wikiRepo, wikiRepoPath, cleanupFunc := setupWikiRepo(t)
	defer cleanupFunc()

	ctx, cancel := testhelper.Context()
	defer cancel()

	server, serverSocketPath := runWikiServiceServer(t)
	defer server.Stop()

	client, conn := newWikiClient(t, serverSocketPath)
	defer conn.Close()

	writeWikiPage(t, client, wikiRepo, createWikiPageOpts{title: "Instálling Gitaly", content: []byte("foobar")})

	content := bytes.Repeat([]byte("Mock wiki page content"), 10000)

	authorID := int32(1)
	authorUserName := []byte("ahmad")
	authorName := []byte("Ahmad Sherif")
	authorEmail := []byte("ahmad@gitlab.com")
	message := []byte("Add installation instructions")

	request := &pb.WikiUpdatePageRequest{
		Repository: wikiRepo,
		PagePath:   []byte("//Instálling Gitaly"),
		Title:      []byte("Instálling Gitaly"),
		Format:     "markdown",
		CommitDetails: &pb.WikiCommitDetails{
			Name:     authorName,
			Email:    authorEmail,
			Message:  message,
			UserId:   authorID,
			UserName: authorUserName,
		},
	}

	stream, err := client.WikiUpdatePage(ctx)
	require.NoError(t, err)

	nSends, err := sendBytes(content, 1000, func(p []byte) error {
		request.Content = p

		if err := stream.Send(request); err != nil {
			return err
		}

		// Use a new response so we don't send other fields (Repository, ...) over and over
		request = &pb.WikiUpdatePageRequest{}

		return nil
	})

	require.NoError(t, err)
	require.True(t, nSends > 1, "should have sent more than one message")

	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	headID := testhelper.MustRunCommand(t, nil, "git", "-C", wikiRepoPath, "show", "--format=format:%H", "--no-patch", "HEAD")
	commit, err := gitlog.GetCommit(ctx, wikiRepo, string(headID), "")
	require.NoError(t, err, "look up git commit before merge is applied")

	require.Equal(t, authorName, commit.Author.Name, "author name mismatched")
	require.Equal(t, authorEmail, commit.Author.Email, "author email mismatched")
	require.Equal(t, message, commit.Subject, "message mismatched")

	pageContent := testhelper.MustRunCommand(t, nil, "git", "-C", wikiRepoPath, "cat-file", "blob", "HEAD:Instálling-Gitaly.md")
	require.Equal(t, content, pageContent, "mismatched content")
}

func TestFailedWikiUpdatePageDueToValidations(t *testing.T) {
	wikiRepo, _, cleanupFunc := setupWikiRepo(t)
	defer cleanupFunc()

	server, serverSocketPath := runWikiServiceServer(t)
	defer server.Stop()

	client, conn := newWikiClient(t, serverSocketPath)
	defer conn.Close()

	writeWikiPage(t, client, wikiRepo, createWikiPageOpts{title: "Installing Gitaly", content: []byte("foobar")})

	commitDetails := &pb.WikiCommitDetails{
		Name:     []byte("Ahmad Sherif"),
		Email:    []byte("ahmad@gitlab.com"),
		Message:  []byte("Add installation instructions"),
		UserId:   int32(1),
		UserName: []byte("ahmad"),
	}

	testCases := []struct {
		desc    string
		request *pb.WikiUpdatePageRequest
		code    codes.Code
	}{
		{
			desc: "empty page_path",
			request: &pb.WikiUpdatePageRequest{
				Repository:    wikiRepo,
				Title:         []byte("Installing Gitaly"),
				Format:        "markdown",
				CommitDetails: commitDetails,
				Content:       []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty title",
			request: &pb.WikiUpdatePageRequest{
				Repository:    wikiRepo,
				PagePath:      []byte("//Installing Gitaly"),
				Format:        "markdown",
				CommitDetails: commitDetails,
				Content:       []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty format",
			request: &pb.WikiUpdatePageRequest{
				Repository:    wikiRepo,
				PagePath:      []byte("//Installing Gitaly.md"),
				Title:         []byte("Installing Gitaly"),
				CommitDetails: commitDetails,
				Content:       []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				Content:    []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' name",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Email:    []byte("a@b.com"),
					Message:  []byte("A message"),
					UserId:   int32(1),
					UserName: []byte("username"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' email",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:     []byte("A name"),
					Message:  []byte("A message"),
					UserId:   int32(1),
					UserName: []byte("username"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' message",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:     []byte("A name"),
					Email:    []byte("a@b.com"),
					UserId:   int32(1),
					UserName: []byte("username"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' user id",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:     []byte("A name"),
					Email:    []byte("a@b.com"),
					Message:  []byte("A message"),
					UserName: []byte("username"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' username",
			request: &pb.WikiUpdatePageRequest{
				Repository: wikiRepo,
				PagePath:   []byte("//Installing Gitaly.md"),
				Title:      []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:    []byte("A name"),
					Email:   []byte("a@b.com"),
					Message: []byte("A message"),
					UserId:  int32(1),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			stream, err := client.WikiUpdatePage(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(testCase.request))

			_, err = stream.CloseAndRecv()
			testhelper.AssertGrpcError(t, err, testCase.code, "")
		})
	}
}
