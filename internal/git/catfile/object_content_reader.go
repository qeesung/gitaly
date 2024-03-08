package catfile

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
)

// ObjectContentReader is a reader for Git objects.
type ObjectContentReader interface {
	cacheable

	// Reader returns a new Object for the given revision. The Object must be fully consumed
	// before another object is requested.
	Object(context.Context, git.Revision) (*Object, error)

	// Queue returns an Queue that can be used to batch multiple object requests.
	// Using the queue is more efficient than using `Object()` when requesting a bunch of
	// objects. The returned function must be executed after use of the Queue has
	// finished.
	Queue(context.Context) (Queue, func(), error)
}

// Object represents data returned by `git cat-file --batch`
type Object struct {
	// ObjectInfo represents main information about object
	ObjectInfo

	// dataReader is reader which has all the object data.
	dataReader io.Reader
}

func (o *Object) Read(p []byte) (int, error) {
	return o.dataReader.Read(p)
}

// WriteTo implements the io.WriterTo interface. It defers the write to the embedded object reader
// via `io.Copy()`, which in turn will use `WriteTo()` or `ReadFrom()` in case these interfaces are
// implemented by the respective reader or writer.
func (o *Object) WriteTo(w io.Writer) (int64, error) {
	// `io.Copy()` will make use of `ReadFrom()` in case the writer implements it.
	return io.Copy(w, o.dataReader)
}
