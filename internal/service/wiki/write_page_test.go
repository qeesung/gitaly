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

func TestSuccessfulWikiWritePageRequest(t *testing.T) {
	wikiRepo, wikiRepoPath, cleanupFunc := setupWikiRepo(t)
	defer cleanupFunc()

	ctx, cancel := testhelper.Context()
	defer cancel()

	server, serverSocketPath := runWikiServiceServer(t)
	defer server.Stop()

	client, conn := newWikiClient(t, serverSocketPath)
	defer conn.Close()

	content := bytes.Repeat([]byte("Mock wiki page content"), 10000)

	authorName := []byte("Ahmad Sherif")
	authorEmail := []byte("ahmad@gitlab.com")
	message := []byte("Add installation instructions")

	request := &pb.WikiWritePageRequest{
		Repository: wikiRepo,
		Name:       []byte("Instálling Gitaly"),
		Format:     "markdown",
		CommitDetails: &pb.WikiCommitDetails{
			Name:    authorName,
			Email:   authorEmail,
			Message: message,
		},
	}

	stream, err := client.WikiWritePage(ctx)
	require.NoError(t, err)

	nSends, err := sendBytes(content, 1000, func(p []byte) error {
		request.Content = p

		if err := stream.Send(request); err != nil {
			return err
		}

		// Use a new response so we don't send other fields (Repository, ...) over and over
		request = &pb.WikiWritePageRequest{}

		return nil
	})

	require.NoError(t, err)
	require.True(t, nSends > 1, "should have sent more than one message")

	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	headID := testhelper.MustRunCommand(t, nil, "git", "-C", wikiRepoPath, "show", "--format=format:%H", "--no-patch", "HEAD")
	commit, err := gitlog.GetCommit(ctx, wikiRepo, string(headID), "")
	require.NoError(t, err, "look up git commit after writing a wiki page")

	require.Equal(t, authorName, commit.Author.Name, "author name mismatched")
	require.Equal(t, authorEmail, commit.Author.Email, "author email mismatched")
	require.Equal(t, message, commit.Subject, "message mismatched")

	pageContent := testhelper.MustRunCommand(t, nil, "git", "-C", wikiRepoPath, "cat-file", "blob", "HEAD:Instálling-Gitaly.md")
	require.Equal(t, content, pageContent, "mismatched content")
}

func TestFailedWikiWritePageDueToDuplicatePage(t *testing.T) {
	wikiRepo, _, cleanupFunc := setupWikiRepo(t)
	defer cleanupFunc()

	server, serverSocketPath := runWikiServiceServer(t)
	defer server.Stop()

	client, conn := newWikiClient(t, serverSocketPath)
	defer conn.Close()

	pageName := "Installing Gitaly"
	content := []byte("Mock wiki page content")
	commitDetails := &pb.WikiCommitDetails{
		Name:    []byte("Ahmad Sherif"),
		Email:   []byte("ahmad@gitlab.com"),
		Message: []byte("Add " + pageName),
	}

	writeWikiPage(t, client, wikiRepo, createWikiPageOpts{title: pageName, content: content})

	request := &pb.WikiWritePageRequest{
		Repository:    wikiRepo,
		Name:          []byte(pageName),
		Format:        "markdown",
		CommitDetails: commitDetails,
		Content:       content,
	}

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.WikiWritePage(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(request))

	response, err := stream.CloseAndRecv()
	require.NoError(t, err)

	expectedResponse := &pb.WikiWritePageResponse{DuplicateError: []byte("Cannot write //Installing-Gitaly.md, found //Installing-Gitaly.md.")}
	require.Equal(t, expectedResponse, response, "mismatched response")
}

func TestFailedWikiWritePageDueToValidations(t *testing.T) {
	wikiRepo := &pb.Repository{}

	server, serverSocketPath := runWikiServiceServer(t)
	defer server.Stop()

	client, conn := newWikiClient(t, serverSocketPath)
	defer conn.Close()

	commitDetails := &pb.WikiCommitDetails{
		Name:    []byte("Ahmad Sherif"),
		Email:   []byte("ahmad@gitlab.com"),
		Message: []byte("Add installation instructions"),
	}

	testCases := []struct {
		desc    string
		request *pb.WikiWritePageRequest
		code    codes.Code
	}{
		{
			desc: "empty name",
			request: &pb.WikiWritePageRequest{
				Repository:    wikiRepo,
				Format:        "markdown",
				CommitDetails: commitDetails,
				Content:       []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty format",
			request: &pb.WikiWritePageRequest{
				Repository:    wikiRepo,
				Name:          []byte("Installing Gitaly"),
				CommitDetails: commitDetails,
				Content:       []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details",
			request: &pb.WikiWritePageRequest{
				Repository: wikiRepo,
				Name:       []byte("Installing Gitaly"),
				Format:     "markdown",
				Content:    []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' name",
			request: &pb.WikiWritePageRequest{
				Repository: wikiRepo,
				Name:       []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Email:   []byte("a@b.com"),
					Message: []byte("A message"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' email",
			request: &pb.WikiWritePageRequest{
				Repository: wikiRepo,
				Name:       []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:    []byte("A name"),
					Message: []byte("A message"),
				},
				Content: []byte(""),
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty commit details' message",
			request: &pb.WikiWritePageRequest{
				Repository: wikiRepo,
				Name:       []byte("Installing Gitaly"),
				Format:     "markdown",
				CommitDetails: &pb.WikiCommitDetails{
					Name:  []byte("A name"),
					Email: []byte("a@b.com"),
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

			stream, err := client.WikiWritePage(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(testCase.request))

			_, err = stream.CloseAndRecv()
			testhelper.AssertGrpcError(t, err, testCase.code, "")
		})
	}
}
