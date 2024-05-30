package cfgerror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/exp/constraints"
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
	// ErrNotAbsolutePath should be used in case absolute path is expected, but the relative was provided.
	ErrNotAbsolutePath = errors.New("not an absolute path")
	// ErrNotUnique should be used when the value must be unique, but there are duplicates.
	ErrNotUnique = errors.New("not unique")
	// ErrBadOrder should be used when the order of the elements is wrong.
	ErrBadOrder = errors.New("bad order")
	// ErrNotInRange should be used when the value is not in expected range of values.
	ErrNotInRange = errors.New("not in range")
	// ErrUnsupportedValue should be used when the value is not supported.
	ErrUnsupportedValue = errors.New("not supported")
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
	if err == nil {
		return vs
	}

	var validationErrs ValidationErrors
	var validationErr ValidationError

	if errors.As(err, &validationErrs) {
		for _, err := range validationErrs {
			vs = append(vs, ValidationError{
				Key:   append(keys, err.Key...),
				Cause: err.Cause,
			})
		}
	} else if errors.As(err, &validationErr) {
		vs = append(vs, ValidationError{
			Key:   append(keys, validationErr.Key...),
			Cause: validationErr.Cause,
		})
	} else {
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

// PathIsAbs checks if provided path is an absolute path.
func PathIsAbs(path string) error {
	if filepath.IsAbs(path) {
		return nil
	}
	return NewValidationError(fmt.Errorf("%w: %q", ErrNotAbsolutePath, path))
}

// InRangeOpt represents configuration options for InRange function.
type InRangeOpt int

const (
	// InRangeOptIncludeMin includes min value equality.
	InRangeOptIncludeMin InRangeOpt = iota + 1
	// InRangeOptIncludeMax includes max value equality.
	InRangeOptIncludeMax
)

type inRangeOpts[T Numeric] []InRangeOpt

func (opts inRangeOpts[T]) lessThan(val, min T) bool {
	for _, opt := range opts {
		if opt == InRangeOptIncludeMin {
			return val < min
		}
	}

	return val <= min
}

func (opts inRangeOpts[T]) greaterThan(val, max T) bool {
	for _, opt := range opts {
		if opt == InRangeOptIncludeMax {
			return val > max
		}
	}

	return val >= max
}

func (opts inRangeOpts[T]) formatRange(min, max T) string {
	return opts.formatRangeMin(min) + ", " + opts.formatRangeMax(max)
}

func (opts inRangeOpts[T]) formatRangeMin(min T) string {
	for _, opt := range opts {
		if opt == InRangeOptIncludeMin {
			return fmt.Sprintf("[%v", min)
		}
	}
	return fmt.Sprintf("(%v", min)
}

func (opts inRangeOpts[T]) formatRangeMax(max T) string {
	for _, opt := range opts {
		if opt == InRangeOptIncludeMax {
			return fmt.Sprintf("%v]", max)
		}
	}
	return fmt.Sprintf("%v)", max)
}

// Numeric includes types that can be used in the comparison operations.
type Numeric interface {
	constraints.Integer | constraints.Float
}

// InRange returns an error if 'val' is less than 'min' or greater or equal to 'max'.
func InRange[T Numeric](min, max, val T, opts ...InRangeOpt) error {
	if cmp := inRangeOpts[T](opts); cmp.lessThan(val, min) || cmp.greaterThan(val, max) {
		return NewValidationError(fmt.Errorf("%w: %v out of %s", ErrNotInRange, val, cmp.formatRange(min, max)))
	}

	return nil
}

// IsSupportedValue ensures the provided 'value' is one listed as 'supportedValues'.
func IsSupportedValue[T comparable](value T, supportedValues ...T) error {
	for _, supportedValue := range supportedValues {
		if value == supportedValue {
			return nil
		}
	}

	if reflect.TypeOf(value).Kind() == reflect.String {
		return NewValidationError(fmt.Errorf(`%w: "%v"`, ErrUnsupportedValue, value))
	}

	return NewValidationError(fmt.Errorf("%w: %v", ErrUnsupportedValue, value))
}

type numeric[T Numeric] struct {
	value T
}

// Comparable wraps value, so the method can be invoked on it.
func Comparable[T Numeric](val T) numeric[T] {
	return numeric[T]{value: val}
}

// LessThan returns an error if val is less than one hold by c.
func (c numeric[T]) LessThan(val T) error {
	if cmp := inRangeOpts[T](nil); cmp.lessThan(val, c.value) {
		err := fmt.Errorf("%w: %v is not less than %v", ErrNotInRange, c.value, val)
		return NewValidationError(err)
	}
	return nil
}

// GreaterThan returns an error if val is greater than one hold by c.
func (c numeric[T]) GreaterThan(val T) error {
	if cmp := inRangeOpts[T](nil); cmp.greaterThan(val, c.value) {
		err := fmt.Errorf("%w: %v is not greater than %v", ErrNotInRange, c.value, val)
		return NewValidationError(err)
	}
	return nil
}

// GreaterOrEqual returns an error if val is greater than one hold by c.
func (c numeric[T]) GreaterOrEqual(val T) error {
	if cmp := (inRangeOpts[T]{InRangeOptIncludeMax}); cmp.greaterThan(val, c.value) {
		err := fmt.Errorf("%w: %v is not greater than or equal to %v", ErrNotInRange, c.value, val)
		return NewValidationError(err)
	}
	return nil
}

// InRange returns an error if 'c.value' is less than 'min' or greater or equal to 'max'.
func (c numeric[T]) InRange(min, max T, opts ...InRangeOpt) error {
	return InRange(min, max, c.value, opts...)
}

// NotEmptySlice returns an error if provided slice has no elements.
func NotEmptySlice[T any](slice []T) error {
	if len(slice) == 0 {
		return NewValidationError(ErrNotSet)
	}
	return nil
}

// NotEmptyMap returns an error if provided map has no elements.
func NotEmptyMap[K comparable, V any](m map[K]V) error {
	if len(m) == 0 {
		return NewValidationError(ErrNotSet)
	}
	return nil
}
