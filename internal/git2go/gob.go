package git2go

import (
	"encoding/gob"
	"errors"
	"reflect"
)

func init() {
	for typee := range registeredTypes {
		gob.Register(typee)
	}
}

var registeredTypes = map[interface{}]struct{}{
	ChangeFileMode{}:         {},
	CreateDirectory{}:        {},
	CreateFile{}:             {},
	DeleteFile{}:             {},
	MoveFile{}:               {},
	UpdateFile{}:             {},
	wrapError{}:              {},
	DirectoryExistsError(""): {},
	FileExistsError(""):      {},
	FileNotFoundError(""):    {},
	InvalidArgumentError(""): {},
	RevertConflictError{}:    {},
	ApplyConflictError{}:     {},
}

// Result is the serialized result.
type Result struct {
	// CommitID is the result of the call.
	CommitID string
	// Error is set if the call errord.
	Error error
}

// wrapError is used to serialize wrapped errors as fmt.wrapError type only has
// private fields and can't be serialized via gob. It's also used to serialize unregistered
// error types by serializing only their error message.
type wrapError struct {
	Message string
	Err     error
}

func (err wrapError) Error() string { return err.Message }

func (err wrapError) Unwrap() error { return err.Err }

// SerializableError returns an error that is Gob serializable.
// Registered types are serialized directly. Unregistered types
// are transformed in to an opaque error using their error message.
// Wrapped errors remain unwrappable.
func SerializableError(err error) error {
	if err == nil {
		return nil
	}

	if unwrappedErr := errors.Unwrap(err); unwrappedErr != nil {
		return wrapError{
			Message: err.Error(),
			Err:     SerializableError(unwrappedErr),
		}
	}

	if _, ok := registeredTypes[reflect.Zero(reflect.TypeOf(err)).Interface()]; !ok {
		return wrapError{Message: err.Error()}
	}

	return err
}
