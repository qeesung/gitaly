package catfile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

const (
	// contentsCommand is the command expected by the `--batch-command` mode of git-cat-file(1)
	// for reading an objects contents.
	contentsCommand = "contents"
	// infoCommand is the command expected by the `--batch-command` mode of git-cat-file(1)
	// for reading an objects info.
	infoCommand = "info"
	// flushCommand is the command expected by the `--batch-command` mode of git-cat-file(1)
	// for flushing out to stdout.
	flushCommand = "flush"
	// flushCommandHack is the command we send to git-cat-file(1) to cause it to flush its stdout.
	// Note that this is a hack: git-cat-file(1) doesn't really support flushing, but it will
	// flush whenever it encounters an object it doesn't know. The flush command we use is thus
	// chosen such that it cannot ever refer to a valid object: refs may not contain whitespace,
	// so this command cannot refer to a ref. Adding "FLUSH" is just for the sake of making it
	// easier to spot what's going on in case we ever mistakenly see this output in the wild.
	flushCommandHack = "\tFLUSH\t"
)

type queueCounters struct {
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
}

type requestQueue struct {
	// queueCounters is a separate structure to hold variables accessed with sync/atomic
	// to ensure 64-bit alignment. This needs to be listed first.
	// This explicit ordering can go away once we use Go 1.19's atomic types: https://gitlab.com/gitlab-org/gitaly/-/issues/4702
	counters queueCounters

	// objectHash is the object hash used by the repository the request queue has been
	// spawned for.
	objectHash git.ObjectHash

	stdout *bufio.Reader
	stdin  *bufio.Writer

	// trace is the current tracing span.
	trace *trace
}

// isDirty returns true either if there are outstanding requests for objects or if the current
// object hasn't yet been fully consumed.
func (q *requestQueue) isDirty() bool {
	if atomic.LoadInt32(&q.counters.isReadingObject) != 0 {
		return true
	}

	if atomic.LoadInt64(&q.counters.outstandingRequests) != 0 {
		return true
	}

	return false
}

func (q *requestQueue) isClosed() bool {
	return atomic.LoadInt32(&q.counters.closed) == 1
}

func (q *requestQueue) close() {
	atomic.StoreInt32(&q.counters.closed, 1)
}

// RequestObject requests the contents for the given revision. A subsequent call has
// to be made to ReadObject to read the contents.
func (q *requestQueue) RequestObject(ctx context.Context, revision git.Revision) error {
	defer logDuration(ctx, "request_object")()

	return q.requestRevision(ctx, contentsCommand, revision)
}

// RequestObject requests the info for the given revision. A subsequent call has to
// be made to ReadInfo read the info.
func (q *requestQueue) RequestInfo(ctx context.Context, revision git.Revision) error {
	defer logDuration(ctx, "request_info")()

	return q.requestRevision(ctx, infoCommand, revision)
}

func (q *requestQueue) requestRevision(ctx context.Context, cmd string, revision git.Revision) error {
	if strings.Contains(revision.String(), "\000") {
		return structerr.NewInvalidArgument("revision must not contain NUL bytes")
	}

	if q.isClosed() {
		return fmt.Errorf("cannot request revision: %w", os.ErrClosed)
	}

	atomic.AddInt64(&q.counters.outstandingRequests, 1)

	input := cmd + " " + revision.String()

	if _, err := q.stdin.WriteString(input); err != nil {
		atomic.AddInt64(&q.counters.outstandingRequests, -1)
		return fmt.Errorf("writing object request: %w", err)
	}

	if err := q.stdin.WriteByte('\000'); err != nil {
		atomic.AddInt64(&q.counters.outstandingRequests, -1)
		return fmt.Errorf("terminating object request: %w", err)
	}

	return nil
}

func (q *requestQueue) Flush(ctx context.Context) error {
	defer logDuration(ctx, "flush")()

	if q.isClosed() {
		return fmt.Errorf("cannot flush: %w", os.ErrClosed)
	}

	if _, err := q.stdin.WriteString(flushCommand); err != nil {
		return fmt.Errorf("writing flush command: %w", err)
	}

	if err := q.stdin.WriteByte('\000'); err != nil {
		return fmt.Errorf("terminating flush command: %w", err)
	}

	if err := q.stdin.Flush(); err != nil {
		return fmt.Errorf("flushing: %w", err)
	}

	return nil
}

type readerFunc func([]byte) (int, error)

func (fn readerFunc) Read(buf []byte) (int, error) { return fn(buf) }

func (q *requestQueue) ReadObject(ctx context.Context) (*Object, error) {
	defer logDuration(ctx, "read_object")()

	// We need to ensure that only a single call to `ReadObject()` can happen at the
	// same point in time.
	//
	// Note that this must happen before we read the current object: otherwise, a
	// concurrent caller might've already swapped it out under our feet.
	if !atomic.CompareAndSwapInt32(&q.counters.isReadingObject, 0, 1) {
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
		if errors.As(err, &NotFoundError{}) {
			atomic.StoreInt32(&q.counters.isReadingObject, 0)
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
				return 0, fmt.Errorf("discard newline: %w", err)
			}

			atomic.StoreInt32(&q.counters.isReadingObject, 0)

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

func (q *requestQueue) ReadInfo(ctx context.Context) (*ObjectInfo, error) {
	defer logDuration(ctx, "read_info")()

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
	queuedRequests := atomic.LoadInt64(&q.counters.outstandingRequests)
	if queuedRequests == 0 {
		return nil, fmt.Errorf("no outstanding request")
	}

	// We cannot check whether `outstandingRequests` is strictly smaller than before given
	// that there may be a concurrent caller who requests additional objects, which would in
	// turn increment the counter. But we can at least verify that it's not smaller than what we
	// expect to give us the chance to detect concurrent reads.
	if atomic.AddInt64(&q.counters.outstandingRequests, -1) < queuedRequests-1 {
		return nil, fmt.Errorf("concurrent read on request queue")
	}

	return ParseObjectInfo(q.objectHash, q.stdout, true)
}

func logDuration(ctx context.Context, logFieldName string) func() {
	start := time.Now()
	return func() {
		delta := time.Since(start)
		if customFields := log.CustomFieldsFromContext(ctx); customFields != nil {
			customFields.RecordSum(fmt.Sprintf("catfile.%s_count", logFieldName), 1)
			customFields.RecordSum(fmt.Sprintf("catfile.%s_ms", logFieldName), int(delta.Milliseconds()))
			customFields.RecordSum("catfile.duration_ms", int(delta.Milliseconds()))
		}
	}
}
