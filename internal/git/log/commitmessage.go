package log

import (
	"context"
	"fmt"
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

	c, err := catfile.New(ctx, repo)
	if err != nil {
		return nil, err
	}

	go func() {
		select {
		case cmh.catFileErr <- cmh.handleCatFile(c):
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
func (cmh *commitMessageHelper) handleCatFile(c *catfile.Batch) error {
	for {
		select {
		case messageRequest := <-cmh.requests:
			subject, body, err := getCommitMessage(c, messageRequest.commitID)

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
func getCommitMessage(c *catfile.Batch, commitID string) (string, string, error) {
	objInfo, err := c.Info(commitID)
	if err != nil {
		return "", "", err
	}

	if objInfo.Oid == "" || objInfo.Type != "commit" {
		return "", "", fmt.Errorf("invalid ObjectInfo for %q: %v", commitID, objInfo)
	}

	commitReader, err := c.Commit(objInfo.Oid)
	if err != nil {
		return "", "", err
	}

	rawCommit, err := ioutil.ReadAll(commitReader)
	if err != nil {
		return "", "", err
	}

	var body string
	if split := strings.SplitN(string(rawCommit), "\n\n", 2); len(split) == 2 {
		body = split[1]
	}
	subject := strings.TrimRight(strings.SplitN(body, "\n", 2)[0], "\r\n")

	return subject, body, nil
}
