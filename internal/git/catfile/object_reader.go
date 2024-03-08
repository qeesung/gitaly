package catfile

import (
	"bufio"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
)

// ObjectReader returns information about an object referenced by a given revision.
type ObjectReader interface {
	cacheable

	// Info returns object information for the given revision.
	Info(context.Context, git.Revision) (*ObjectInfo, error)

	// Object returns a new Object for the given revision. The Object must be fully consumed
	// before another object is requested.
	Object(context.Context, git.Revision) (*Object, error)

	// ObjectQueue returns an ObjectQueue that can be used to batch multiple object requests.
	// Using the queue is more efficient than using `Object()` when requesting a bunch of
	// objects. The returned function must be executed after use of the ObjectQueue has
	// finished. Object Content and information can be requested from the queue but their
	// respective ordering must be maintained.
	ObjectQueue(context.Context) (ObjectQueue, func(), error)
}

// ObjectQueue allows for requesting and reading objects independently of each other. The number of
// RequestObject+RequestInfo and ReadObject+RequestInfo calls must match and their ordering must be
// maintained. ReadObject/ReadInfo must be executed after the object has been requested already.
// The order of objects returned by ReadObject/ReadInfo is the same as the order in
// which objects have been requested. Users of this interface must call `Flush()` after all requests
// have been queued up such that all requested objects will be readable.
type ObjectQueue interface {
	// RequestObject requests the given revision from git-cat-file(1).
	RequestObject(context.Context, git.Revision) error
	// ReadObject reads an object which has previously been requested.
	ReadObject(context.Context) (*Object, error)
	// RequestInfo requests the given revision from git-cat-file(1).
	RequestInfo(context.Context, git.Revision) error
	// ReadInfo reads object info which has previously been requested.
	ReadInfo(context.Context) (*ObjectInfo, error)
	// Flush flushes all queued requests and asks git-cat-file(1) to print all objects which
	// have been requested up to this point.
	Flush(context.Context) error
}

// objectReader is a reader for Git objects. Reading is implemented via a long-lived `git cat-file
// --batch-command` process such that we do not have to spawn a new process for each object we
// are about to read.
type objectReader struct {
	cmd *command.Command

	counter *prometheus.CounterVec

	q          requestQueue
	queueInUse int32
}

func newObjectReader(
	ctx context.Context,
	repo git.RepositoryExecutor,
	counter *prometheus.CounterVec,
) (*objectReader, error) {
	flags := []git.Option{
		git.Flag{Name: "-Z"},
		git.Flag{Name: "--batch-command"},
		git.Flag{Name: "--buffer"},
	}

	if featureflag.MailmapOptions.IsEnabled(ctx) {
		flags = append([]git.Option{git.Flag{Name: "--use-mailmap"}}, flags...)
	}

	batchCmd, err := repo.Exec(ctx,
		git.Command{
			Name:  "cat-file",
			Flags: flags,
		},
		git.WithSetupStdin(),
		git.WithSetupStdout(),
	)
	if err != nil {
		return nil, err
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	objectReader := &objectReader{
		cmd:     batchCmd,
		counter: counter,
		q: requestQueue{
			objectHash: objectHash,
			stdout:     bufio.NewReader(batchCmd),
			stdin:      bufio.NewWriter(batchCmd),
		},
	}

	return objectReader, nil
}

func (o *objectReader) close() {
	o.q.close()
	_ = o.cmd.Wait()
}

func (o *objectReader) isClosed() bool {
	return o.q.isClosed()
}

func (o *objectReader) isDirty() bool {
	if atomic.LoadInt32(&o.queueInUse) != 0 {
		return true
	}

	return o.q.isDirty()
}

func (o *objectReader) queue(ctx context.Context, tracedMethod string) (*requestQueue, func(), error) {
	if !atomic.CompareAndSwapInt32(&o.queueInUse, 0, 1) {
		return nil, nil, fmt.Errorf("object queue already in use")
	}

	trace := startTrace(ctx, o.counter, tracedMethod)
	o.q.trace = trace

	return &o.q, func() {
		atomic.StoreInt32(&o.queueInUse, 0)
		trace.finish()
	}, nil
}

// Object returns a new Object for the given revision. The Object must be fully consumed
// before another object is requested.
func (o *objectReader) Object(ctx context.Context, revision git.Revision) (*Object, error) {
	queue, finish, err := o.queue(ctx, "catfile.Object")
	if err != nil {
		return nil, err
	}
	defer finish()

	if err := queue.RequestObject(ctx, revision); err != nil {
		return nil, err
	}

	if err := queue.Flush(ctx); err != nil {
		return nil, err
	}

	object, err := queue.ReadObject(ctx)
	if err != nil {
		return nil, err
	}

	return object, nil
}

// ObjectQueue returns an ObjectQueue that can be used to batch multiple object requests.
// Using the queue is more efficient than using `Object()` when requesting a bunch of
// objects. The returned function must be executed after use of the ObjectQueue has
// finished. Object Content and information can be requested from the queue but their
// respective ordering must be maintained.
func (o *objectReader) ObjectQueue(ctx context.Context) (ObjectQueue, func(), error) {
	queue, finish, err := o.queue(ctx, "catfile.ObjectQueue")
	if err != nil {
		return nil, nil, err
	}
	return queue, finish, nil
}

// Info returns object information for the given revision.
func (o *objectReader) Info(ctx context.Context, revision git.Revision) (*ObjectInfo, error) {
	queue, cleanup, err := o.queue(ctx, "catfile.Info")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := queue.RequestInfo(ctx, revision); err != nil {
		return nil, err
	}

	if err := queue.Flush(ctx); err != nil {
		return nil, err
	}

	objectInfo, err := queue.ReadInfo(ctx)
	if err != nil {
		return nil, err
	}

	return objectInfo, nil
}
