package validator

import (
	"errors"
	"fmt"
	"strings"
)

// Error combines a key with an erorr. The Key is the path to the field
// within the struct.
type Error struct {
	Key   []string
	Cause error
}

// NewError wraps the error with a key so it's location in the struct
// can be propagated upwards.
func NewError(key string, err error) error {
	if key == "" {
		// There's no reason to annotate something with an empty key. This
		// handles not adding a key for the root node.
		return err
	}

	var keyedErr Error
	if errors.As(err, &keyedErr) {
		// If the error is a KeyedError, prepend the key to the existing key.
		keyedErr.Key = append([]string{key}, keyedErr.Key...)
		return keyedErr
	}

	return Error{Key: []string{key}, Cause: err}
}

// Error returns the error message.
func (err Error) Error() string {
	return fmt.Sprintf("%s: %s", strings.Join(err.Key, "."), err.Cause)
}

// Unwrap the underlying error.
func (err Error) Unwrap() error {
	return err.Cause
}

// Errors is a collection of multiple errors.
type Errors []error

// Append adds an error to errs. If the err is Errors type itself,
// it's flattened and each error appended to errs.
func (errs Errors) Append(err error) Errors {
	if otherErrs, ok := err.(Errors); ok {
		return append(errs, otherErrs...)
	}

	return append(errs, err)
}

// Error returns the error message.
func (errs Errors) Error() string {
	var str []string
	for _, err := range errs {
		str = append(str, err.Error())
	}

	return strings.Join(str, "\n")
}

// ErrorOrNil returns an error if there are some errors, nil otherwise.
// This should be used instead of returning a typed error to avoid
// nil interface comparison problems.
func (errs Errors) ErrorOrNil() error {
	if len(errs) == 0 {
		return nil
	}

	return errs
}
