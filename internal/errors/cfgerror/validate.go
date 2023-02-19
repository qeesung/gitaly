package cfgerror

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

var (
	// ErrNotSet should be used when the value is not set, but it is required.
	ErrNotSet = errors.New("not set")
	// ErrBlankOrEmpty should be used when non-blank/non-empty string is expected.
	ErrBlankOrEmpty = errors.New("blank or empty")
	// ErrDoesntExist should be used when resource doesn't exist.
	ErrDoesntExist = errors.New("doesn't exist")
	// ErrNotDir should be used when path on the file system exists, but it is not a directory.
	ErrNotDir = errors.New("not a dir")
	// ErrNotFile should be used when path on the file system exists, but it is not a file.
	ErrNotFile = errors.New("not a file")
	// ErrNotUnique should be used when the value must be unique, but there are duplicates.
	ErrNotUnique = errors.New("not unique")
	// ErrIsNegative should be used when the positive value or 0 is expected.
	ErrIsNegative = errors.New("is negative")
	// ErrBadOrder should be used when the order of the elements is wrong.
	ErrBadOrder = errors.New("bad order")
)

// ValidationError represents an issue with provided configuration.
type ValidationError struct {
	// Key represents a path to the field.
	Key []string
	// Cause contains a reason why validation failed.
	Cause error
}

// Error to implement an error standard interface.
// The string representation can have 3 different formats:
// - when Key and Cause is set: "outer.inner: failure cause"
// - when only Key is set: "outer.inner"
// - when only Cause is set: "failure cause"
func (ve ValidationError) Error() string {
	if len(ve.Key) != 0 && ve.Cause != nil {
		return fmt.Sprintf("%s: %v", strings.Join(ve.Key, "."), ve.Cause)
	}
	if len(ve.Key) != 0 {
		return strings.Join(ve.Key, ".")
	}
	if ve.Cause != nil {
		return fmt.Sprintf("%v", ve.Cause)
	}
	return ""
}

// NewValidationError creates a new ValidationError with provided parameters.
func NewValidationError(err error, keys ...string) ValidationError {
	return ValidationError{Key: keys, Cause: err}
}

// ValidationErrors is a list of ValidationError-s.
type ValidationErrors []ValidationError

// Append adds provided error into current list by enriching each ValidationError with the
// provided keys or if provided err is not an instance of the ValidationError it will be wrapped
// into it. In case the nil is provided nothing happens.
func (vs ValidationErrors) Append(err error, keys ...string) ValidationErrors {
	switch terr := err.(type) {
	case nil:
		return vs
	case ValidationErrors:
		for _, err := range terr {
			vs = append(vs, ValidationError{
				Key:   append(keys, err.Key...),
				Cause: err.Cause,
			})
		}
	case ValidationError:
		vs = append(vs, ValidationError{
			Key:   append(keys, terr.Key...),
			Cause: terr.Cause,
		})
	default:
		vs = append(vs, ValidationError{
			Key:   keys,
			Cause: err,
		})
	}

	return vs
}

// AsError returns nil if there are no elements and itself if there is at least one.
func (vs ValidationErrors) AsError() error {
	if len(vs) != 0 {
		return vs
	}
	return nil
}

// Error transforms all validation errors into a single string joined by newline.
func (vs ValidationErrors) Error() string {
	var buf strings.Builder
	for i, ve := range vs {
		if i != 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(ve.Error())
	}
	return buf.String()
}

// New returns uninitialized ValidationErrors object.
func New() ValidationErrors {
	return nil
}

// NotEmpty checks if value is empty.
func NotEmpty(val string) error {
	if val == "" {
		return NewValidationError(ErrNotSet)
	}
	return nil
}

// NotBlank checks the value is not empty or blank.
func NotBlank(val string) error {
	if strings.TrimSpace(val) == "" {
		return NewValidationError(ErrBlankOrEmpty)
	}
	return nil
}

// DirExists checks the value points to an existing directory on the disk.
func DirExists(path string) error {
	fs, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewValidationError(fmt.Errorf("%w: %q", ErrDoesntExist, path))
		}
		return err
	}

	if !fs.IsDir() {
		return NewValidationError(fmt.Errorf("%w: %q", ErrNotDir, path))
	}

	return nil
}

// FileExists checks the value points to an existing file on the disk.
func FileExists(path string) error {
	fs, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewValidationError(fmt.Errorf("%w: %q", ErrDoesntExist, path))
		}
		return err
	}

	if fs.IsDir() {
		return NewValidationError(fmt.Errorf("%w: %q", ErrNotFile, path))
	}

	return nil
}
