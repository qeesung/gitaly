package catfile

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

const (
	// flushCommand is the command we send to git-cat-file(1) to cause it to flush its stdout.
	// Note that this is a hack: git-cat-file(1) doesn't really support flushing, but it will
	// flush whenever it encounters an object it doesn't know. The flush command we use is thus
	// chosen such that it cannot ever refer to a valid object: refs may not contain whitespace,
	// so this command cannot refer to a ref. Adding "FLUSH" is just for the sake of making it
	// easier to spot what's going on in case we ever mistakenly see this output in the wild.
	flushCommand = "\tFLUSH\t"
)

type requestQueue struct {
	// outstandingRequests is the number of requests which have been queued up. Gets incremented
	// on request, and decremented when starting to read an object (not when that object has
	// been fully consumed).
	//
	// We list the atomic fields first to ensure they are 64-bit and 32-bit aligned:
	// https://pkg.go.dev/sync/atomic#pkg-note-BUG
	outstandingRequests int64

	// closed indicates whether the queue is closed for additional requests.
	closed int32

	// isReadingObject indicates whether there is a read in progress.
	isReadingObject int32

	// isObjectQueue is set to `true` when this is a request queue which can be used for reading
	// objects. If set to `false`, then this can only be used to read object info.
	isObjectQueue bool

	stdout *bufio.Reader
	stdin  *bufio.Writer

	// trace is the current tracing span.
	trace *trace
}

// isDirty returns true either if there are outstanding requests for objects or if the current
// object hasn't yet been fully consumed.
func (q *requestQueue) isDirty() bool {
	if atomic.LoadInt32(&q.isReadingObject) != 0 {
		return true
	}

	if atomic.LoadInt64(&q.outstandingRequests) != 0 {
		return true
	}

	return false
}

func (q *requestQueue) isClosed() bool {
	return atomic.LoadInt32(&q.closed) == 1
}

func (q *requestQueue) close() {
	atomic.StoreInt32(&q.closed, 1)
}

func (q *requestQueue) RequestRevision(revision git.Revision) error {
	if q.isClosed() {
		return fmt.Errorf("cannot request revision: %w", os.ErrClosed)
	}

	atomic.AddInt64(&q.outstandingRequests, 1)

	if _, err := q.stdin.WriteString(revision.String()); err != nil {
		atomic.AddInt64(&q.outstandingRequests, -1)
		return fmt.Errorf("writing object request: %w", err)
	}

	if err := q.stdin.WriteByte('\n'); err != nil {
		atomic.AddInt64(&q.outstandingRequests, -1)
		return fmt.Errorf("terminating object request: %w", err)
	}

	return nil
}

func (q *requestQueue) Flush() error {
	if q.isClosed() {
		return fmt.Errorf("cannot flush: %w", os.ErrClosed)
	}

	if _, err := q.stdin.WriteString(flushCommand); err != nil {
		return fmt.Errorf("writing flush command: %w", err)
	}

	if err := q.stdin.WriteByte('\n'); err != nil {
		return fmt.Errorf("terminating flush command: %w", err)
	}

	if err := q.stdin.Flush(); err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	return nil
}

type readerFunc func([]byte) (int, error)

func (fn readerFunc) Read(buf []byte) (int, error) { return fn(buf) }

func (q *requestQueue) ReadObject() (*Object, error) {
	if !q.isObjectQueue {
		panic("object queue used to read object info")
	}

	// We need to ensure that only a single call to `ReadObject()` can happen at the
	// same point in time.
	//
	// Note that this must happen before we read the current object: otherwise, a
	// concurrent caller might've already swapped it out under our feet.
	if !atomic.CompareAndSwapInt32(&q.isReadingObject, 0, 1) {
		return nil, fmt.Errorf("current object has not been fully read")
	}

	objectInfo, err := q.readInfo()
	if err != nil {
		// In the general case we cannot know why reading the object's info has failed. And
		// given that git-cat-file(1) is stateful, we cannot say whether it's safe to
		// continue reading from it now or whether we need to keep the queue dirty instead.
		// So we keep `isReadingObject == 0` in the general case so that it continues to
		// stay dirty.
		//
		// One known exception is when we've got a NotFoundError: this is a graceful failure
		// and we can continue reading from the process.
		if IsNotFound(err) {
			atomic.StoreInt32(&q.isReadingObject, 0)
		}

		return nil, err
	}
	q.trace.recordRequest(objectInfo.Type)

	// objectReader first reads the object data from stdout. After that, it discards
	// the trailing newline byte that separate the different objects. Finally, it
	// undirties the reader so the next object can be read.
	objectReader := io.MultiReader(
		&io.LimitedReader{
			R: q.stdout,
			N: objectInfo.Size,
		},
		readerFunc(func([]byte) (int, error) {
			if _, err := io.CopyN(io.Discard, q.stdout, 1); err != nil {
				return 0, fmt.Errorf("discard newline: %q", err)
			}

			atomic.StoreInt32(&q.isReadingObject, 0)

			return 0, io.EOF
		}),
	)

	return &Object{
		ObjectInfo: *objectInfo,
		dataReader: readerFunc(func(buf []byte) (int, error) {
			// The tests assert that no data can be read after the queue is closed.
			// Some data could be actually read even after the queue closes due to the buffering.
			// Check here if the queue is closed and refuse to read more if so.
			if q.isClosed() {
				return 0, os.ErrClosed
			}

			return objectReader.Read(buf)
		}),
	}, nil
}

func (q *requestQueue) ReadInfo() (*ObjectInfo, error) {
	if q.isObjectQueue {
		panic("object queue used to read object info")
	}

	objectInfo, err := q.readInfo()
	if err != nil {
		return nil, err
	}
	q.trace.recordRequest("info")

	return objectInfo, nil
}

func (q *requestQueue) readInfo() (*ObjectInfo, error) {
	if q.isClosed() {
		return nil, fmt.Errorf("cannot read object info: %w", os.ErrClosed)
	}

	// We first need to determine whether there are any queued requests at all. If not, then we
	// cannot read anything.
	queuedRequests := atomic.LoadInt64(&q.outstandingRequests)
	if queuedRequests == 0 {
		return nil, fmt.Errorf("no outstanding request")
	}

	// We cannot check whether `outstandingRequests` is strictly smaller than before given
	// that there may be a concurrent caller who requests additional objects, which would in
	// turn increment the counter. But we can at least verify that it's not smaller than what we
	// expect to give us the chance to detect concurrent reads.
	if atomic.AddInt64(&q.outstandingRequests, -1) < queuedRequests-1 {
		return nil, fmt.Errorf("concurrent read on request queue")
	}

	return ParseObjectInfo(q.stdout)
}
