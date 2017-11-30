package log

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"strings"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
)

type commitMessageResponse struct {
	subject string
	body    string
	err     error
}

type commitMessageRequest struct {
	commitID string
	response chan commitMessageResponse
}

type commitMessageHelper struct {
	ctx        context.Context
	requests   chan commitMessageRequest
	catFileErr chan error
}

func newCommitMessageHelper(ctx context.Context, repo *pb.Repository) (*commitMessageHelper, error) {
	cmh := &commitMessageHelper{
		ctx:        ctx,
		requests:   make(chan commitMessageRequest),
		catFileErr: make(chan error),
	}

	go func() {
		select {
		case cmh.catFileErr <- catfile.CatFile(ctx, repo, cmh.handleCatFile):
		case <-ctx.Done():
			// This case is here to ensure this goroutine won't leak. We can't assume
			// someone is listening on the cmh.catFileErr channel.
		}

		close(cmh.catFileErr)
	}()

	return cmh, nil
}

// commitMessage returns the raw binary subject and body for the given commitID.
func (cmh *commitMessageHelper) commitMessage(commitID string) (string, string, error) {
	response := make(chan commitMessageResponse)

	select {
	case cmh.requests <- commitMessageRequest{commitID: commitID, response: response}:
		result := <-response
		return result.subject, result.body, result.err
	case err := <-cmh.catFileErr:
		return "", "", fmt.Errorf("git cat-file is not running: %v", err)
	}
}

// handleCatFile gets the raw commit message for a sequence of commit
// ID's from a git-cat-file process.
func (cmh *commitMessageHelper) handleCatFile(stdin io.Writer, stdout *bufio.Reader) error {
	for {
		select {
		case messageRequest := <-cmh.requests:
			subject, body, err := getCommitMessage(messageRequest.commitID, stdin, stdout)

			// Always return a response, because a client is blocked waiting for it.
			messageRequest.response <- commitMessageResponse{
				subject: subject,
				body:    body,
				err:     err,
			}

			if err != nil {
				// Shut down the current goroutine.
				return err
			}
		case <-cmh.ctx.Done():
			// We need this case because we cannot count on the client to close the
			// requests channel.
			return cmh.ctx.Err()
		}
	}
}

// getCommitMessage returns subject, body, error by querying git cat-file via stdin and stdout.
func getCommitMessage(commitID string, stdin io.Writer, stdout *bufio.Reader) (string, string, error) {
	if _, err := fmt.Fprintln(stdin, commitID); err != nil {
		return "", "", err
	}

	objInfo, err := catfile.ParseObjectInfo(stdout)
	if err != nil {
		return "", "", err
	}

	if objInfo.Oid == "" || objInfo.Type != "commit" {
		return "", "", fmt.Errorf("invalid ObjectInfo for %q: %v", commitID, objInfo)
	}

	rawCommit, err := ioutil.ReadAll(io.LimitReader(stdout, objInfo.Size))
	if err != nil {
		return "", "", err
	}

	if _, err := stdout.Discard(1); err != nil {
		return "", "", fmt.Errorf("error discarding newline: %v", err)
	}

	commitString := string(rawCommit)

	var body string
	if split := strings.SplitN(commitString, "\n\n", 2); len(split) == 2 {
		body = split[1]
	}
	subject := strings.TrimRight(strings.SplitN(body, "\n", 2)[0], "\r\n")

	return subject, body, nil
}
