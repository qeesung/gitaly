package localrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// ObjectType is an Enum for the type of object of
// the ls-tree entry, which can be can be tree, blob or commit
type ObjectType int

// Enum values for ObjectType
const (
	Unknown ObjectType = iota
	Tree
	Blob
	Submodule
)

// ObjectTypeFromString translates a string representation of the object type into an ObjectType enum.
func ObjectTypeFromString(s string) ObjectType {
	switch s {
	case "tree":
		return Tree
	case "blob":
		return Blob
	case "commit":
		return Submodule
	default:
		return Unknown
	}
}

// InvalidObjectError is returned when trying to get an object id that is invalid or does not exist.
type InvalidObjectError string

func (err InvalidObjectError) Error() string { return fmt.Sprintf("invalid object %q", string(err)) }

// ReadObjectInfo attempts to read the object info based on a revision.
func (repo *Repo) ReadObjectInfo(ctx context.Context, rev git.Revision) (*catfile.ObjectInfo, error) {
	infoReader, cleanup, err := repo.catfileCache.ObjectInfoReader(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("getting object info reader: %w", err)
	}
	defer cleanup()

	objectInfo, err := infoReader.Info(ctx, rev)
	if err != nil {
		if errors.As(err, &catfile.NotFoundError{}) {
			return nil, InvalidObjectError(rev)
		}
		return nil, fmt.Errorf("getting object info: %w", err)
	}

	return objectInfo, nil
}

// ReadObject reads an object from the repository's object database. InvalidObjectError
// is returned if the oid does not refer to a valid object.
func (repo *Repo) ReadObject(ctx context.Context, oid git.ObjectID) ([]byte, error) {
	objectReader, cancel, err := repo.catfileCache.ObjectReader(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("create object reader: %w", err)
	}
	defer cancel()

	object, err := objectReader.Object(ctx, oid.Revision())
	if err != nil {
		if errors.As(err, &catfile.NotFoundError{}) {
			return nil, InvalidObjectError(oid.String())
		}
		return nil, fmt.Errorf("get object from reader: %w", err)
	}

	data, err := io.ReadAll(object)
	if err != nil {
		return nil, fmt.Errorf("read object from reader: %w", err)
	}

	return data, nil
}

// BadObjectError is returned when attempting to walk a bad object.
type BadObjectError struct {
	// ObjectID is the object id of the object that was bad.
	ObjectID git.ObjectID
}

// Error returns the error message.
func (err BadObjectError) Error() string {
	return fmt.Sprintf("bad object %q", err.ObjectID)
}

// ObjectReadError is returned when reading an object fails.
type ObjectReadError struct {
	// ObjectID is the object id of the object that git failed to read
	ObjectID git.ObjectID
}

// Error returns the error message.
func (err ObjectReadError) Error() string {
	return fmt.Sprintf("failed reading object %q", err.ObjectID)
}

var (
	regexpBadObjectError  = regexp.MustCompile(`^fatal: bad object ([[:xdigit:]]*)\n$`)
	regexpObjectReadError = regexp.MustCompile(`^error: Could not read ([[:xdigit:]]*)\n`)
)

// WalkUnreachableObjects walks the object graph starting from heads and writes to the output object IDs
// that are included in the walk but unreachable from any of the repository's references. Heads should
// return object IDs separated with a newline. Output is object IDs separated by newlines.
func (repo *Repo) WalkUnreachableObjects(ctx context.Context, heads io.Reader, output io.Writer) error {
	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "rev-list",
			Flags: []git.Option{
				git.Flag{Name: "--objects"},
				git.Flag{Name: "--not"},
				git.Flag{Name: "--all"},
				git.Flag{Name: "--stdin"},
			},
		},
		git.WithStdin(heads),
		git.WithStdout(output),
		git.WithStderr(&stderr),
	); err != nil {
		if matches := regexpBadObjectError.FindSubmatch(stderr.Bytes()); len(matches) > 1 {
			return BadObjectError{ObjectID: git.ObjectID(matches[1])}
		}

		if matches := regexpObjectReadError.FindSubmatch(stderr.Bytes()); len(matches) > 1 {
			return ObjectReadError{ObjectID: git.ObjectID(matches[1])}
		}

		return structerr.New("rev-list: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

// WalkObjects walks the object graph starting from heads and writes to the output the objects it
// sees. Objects that are encountered during the walk and exist in the object database are written
// out as `<oid> <path>\n` with the `<path>` being sometimes present if applicable. Objects that
// were encountered during the walk but do not exist in the object database are written out
// as `?<oid>\n`.
func (repo *Repo) WalkObjects(ctx context.Context, heads io.Reader, output io.Writer) error {
	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "rev-list",
			Flags: []git.Option{
				git.Flag{Name: "--objects"},
				git.Flag{Name: "--missing=print"},
				git.Flag{Name: "--stdin"},
			},
		},
		git.WithStdin(heads),
		git.WithStdout(output),
		git.WithStderr(&stderr),
	); err != nil {
		return structerr.New("rev-list: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

// PackObjects takes in object IDs separated by newlines. It packs the objects into a pack file and
// writes it into the output.
func (repo *Repo) PackObjects(ctx context.Context, objectIDs io.Reader, output io.Writer) error {
	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "pack-objects",
			Flags: []git.Option{
				git.Flag{Name: "-q"},
				git.Flag{Name: "--stdout"},
			},
		},
		git.WithStdin(objectIDs),
		git.WithStderr(&stderr),
		git.WithStdout(output),
	); err != nil {
		return structerr.New("pack objects: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

// UnpackObjects unpacks the objects from the pack file to the repository's object database.
func (repo *Repo) UnpackObjects(ctx context.Context, packFile io.Reader) error {
	stderr := &bytes.Buffer{}
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "unpack-objects",
			Flags: []git.Option{
				git.Flag{Name: "-q"},
			},
		},
		git.WithStdin(packFile),
		git.WithStderr(stderr),
	); err != nil {
		return structerr.New("unpack objects: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}
