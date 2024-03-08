package catfile

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// ObjectInfo represents a header returned by `git cat-file --batch`
type ObjectInfo struct {
	Oid  git.ObjectID
	Type string
	Size int64
	// Format is the object format used by this object, e.g. "sha1" or "sha256".
	Format string
}

// IsBlob returns true if object type is "blob"
func (o *ObjectInfo) IsBlob() bool {
	return o.Type == "blob"
}

// ObjectID is the ID of the object.
func (o *ObjectInfo) ObjectID() git.ObjectID {
	return o.Oid
}

// ObjectType is the type of the object.
func (o *ObjectInfo) ObjectType() string {
	return o.Type
}

// ObjectSize is the size of the object.
func (o *ObjectInfo) ObjectSize() int64 {
	return o.Size
}

// NotFoundError is returned when requesting an object that does not exist.
type NotFoundError struct {
	// Revision is the requested revision that could not be found.
	Revision string
}

func (NotFoundError) Error() string {
	return "object not found"
}

// ErrorMetadata returns the error metadata attached to this error, indicating which revision could not be found.
func (e NotFoundError) ErrorMetadata() []structerr.MetadataItem {
	return []structerr.MetadataItem{
		{Key: "revision", Value: e.Revision},
	}
}

// ParseObjectInfo reads from a reader and parses the data into an ObjectInfo struct with the given
// object hash.
func ParseObjectInfo(objectHash git.ObjectHash, stdout *bufio.Reader, nulTerminated bool) (*ObjectInfo, error) {
restart:
	var terminator byte = '\n'
	if nulTerminated {
		terminator = '\000'
	}

	infoLine, err := stdout.ReadString(terminator)
	if err != nil {
		return nil, fmt.Errorf("read info line: %w", err)
	}

	infoLine = strings.TrimSuffix(infoLine, string(terminator))
	if revision, isMissing := strings.CutSuffix(infoLine, " missing"); isMissing {
		// We use a hack to flush stdout of git-cat-file(1), which is that we request an
		// object that cannot exist. This causes Git to write an error and immediately flush
		// stdout. The only downside is that we need to filter this error here, but that's
		// acceptable while git-cat-file(1) doesn't yet have any way to natively flush.
		if strings.HasPrefix(infoLine, flushCommandHack) {
			goto restart
		}

		return nil, NotFoundError{
			Revision: revision,
		}
	}

	info := strings.Split(infoLine, " ")
	if len(info) != 3 {
		return nil, fmt.Errorf("invalid info line: %q", infoLine)
	}

	oid, err := objectHash.FromHex(info[0])
	if err != nil {
		return nil, fmt.Errorf("parse object ID: %w", err)
	}

	objectSize, err := strconv.ParseInt(info[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse object size: %w", err)
	}

	return &ObjectInfo{
		Oid:    oid,
		Type:   info[1],
		Size:   objectSize,
		Format: objectHash.Format,
	}, nil
}

// ObjectInfoReader returns information about an object referenced by a given revision.
type ObjectInfoReader interface {
	cacheable

	// Info requests information about the revision pointed to by the given revision.
	Info(context.Context, git.Revision) (*ObjectInfo, error)

	// ObjectQueue returns an ObjectQueue that can be used to batch multiple object info
	// requests. Using the queue is more efficient than using `Info()` when requesting a bunch
	// of objects. The returned function must be executed after use of the ObjectQueue has
	// finished.
	ObjectQueue(context.Context) (ObjectQueue, func(), error)
}
