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
//
// A C instance can only serve single request at a time. If you want to
// use it across multiple goroutines you need to add your own locking.
type C struct {
	*batchCheck
	*batch
}

// Info returns an ObjectInfo if spec exists. If spec does not exist the
// error is of type NotFoundError.
func (c *C) Info(revspec string) (*ObjectInfo, error) {
	return c.batchCheck.info(revspec)
}

// Tree returns a raw tree object. It is an error if revspec does not
// point to a tree. To prevent this firstuse Info to resolve the revspec
// and check the object type.
func (c *C) Tree(revspec string) ([]byte, error) {
	r, err := c.batch.reader(revspec, "tree")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Commit returns a raw commit object. It is an error if revspec does not
// point to a commit. To prevent this first use Info to resolve the revspec
// and check the object type.
func (c *C) Commit(revspec string) ([]byte, error) {
	r, err := c.batch.reader(revspec, "commit")
	if err != nil {
		return nil, err
	}

	return ioutil.ReadAll(r)
}

// Blob returns a reader for the requested blob. The entire blob must be
// read before any new objects can be requested from this C instance.
//
// It is an error if revspec does not point to a blob. To prevent this
// first use Info to resolve the revspec and check the object type.
func (c *C) Blob(revspec string) (io.Reader, error) {
	return c.batch.reader(revspec, "blob")
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
