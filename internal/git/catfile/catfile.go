package catfile

import (
	"context"
	"io"
	"io/ioutil"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
)

// C abstracts 'git cat-file --batch' and 'git cat-file --batch-check'.
// It lets you retrieve object metadata and raw objects from a Git repo.
type C struct {
	*batchCheck
	*batch
}

// Info returns an ObjectInfo if spec exists. If spec does not exist the
// error is of type NotFoundError.
func (c *C) Info(spec string) (*ObjectInfo, error) {
	return c.batchCheck.info(spec)
}

// Tree returns a raw tree object.
func (c *C) Tree(treeOid string) ([]byte, error) {
	r, err := c.batch.reader(treeOid, "tree")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Commit returns a raw commit object.
func (c *C) Commit(commitOid string) ([]byte, error) {
	r, err := c.batch.reader(commitOid, "commit")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Blob returns a reader for the requested blob. The entire blob must be
// read before any new objects can be requested from this C instance.
func (c *C) Blob(blobOid string) (io.Reader, error) {
	return c.batch.reader(blobOid, "blob")
}

// New returns a new C instance.
func New(ctx context.Context, repo *pb.Repository) (*C, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, err
	}

	c := &C{}

	c.batch, err = newBatch(ctx, repoPath, env)
	if err != nil {
		return nil, err
	}

	c.batchCheck, err = newBatchCheck(ctx, repoPath, env)
	if err != nil {
		return nil, err
	}

	return c, nil
}
