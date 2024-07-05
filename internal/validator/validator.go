package validator

import (
	"errors"
	"fmt"
	"reflect"

	"golang.org/x/exp/constraints"
)

// Combine combines the passed validation functions into a single function.
// The returned function runs each validation function in order on the value
// and returns the first error it encounters.
func Combine[T any](validationFuncs ...Func[T]) Func[T] {
	return func(value T) error {
		for _, validate := range validationFuncs {
			if err := validate(value); err != nil {
				return err
			}
		}

		return nil
	}
}

// IsSet returns an error if the value is not set.
func IsSet[T any](value T) error {
	if reflect.ValueOf(value).IsZero() {
		return errors.New("must be set")
	}

	return nil
}

// IsOneOf returns a validation function that errors if the value does
// not equal one of the given values.
func IsOneOf[T comparable](values ...T) Func[T] {
	return func(value T) error {
		for _, allowed := range values {
			if value == allowed {
				return nil
			}
		}

		return fmt.Errorf("must be one of %v", values)
	}
}

// Equal returns a validation function that errors if the value does not
// equal the given value.
func Equal[T comparable](validValue T) Func[T] {
	return func(value T) error {
		if value != validValue {
			return fmt.Errorf(`must equal "%v"`, validValue)
		}

		return nil
	}
}

// IsInRange returns a validation function that errors if the value
// is not within the given range. The boundaries are valid values.
func IsInRange[T constraints.Ordered](min, max T) Func[T] {
	return func(value T) error {
		if value < min || max < value {
			return fmt.Errorf("must be in range [%v, %v]", min, max)
		}

		return nil
	}
}

// Field is a validatable field.
type Field[T any] struct {
	Value    T
	validate Func[T]
}

// NewField returns a new Field with the attached validation function. The Value
// field is not included in the reported key path if a child field fails validation.
func NewField[T any](value T, validate Func[T]) Field[T] {
	return Field[T]{
		Value:    value,
		validate: validate,
	}
}

// Validate validates the field.
func (f Field[T]) Validate() error {
	return f.validate(f.Value)
}

// skipTo skips the Field itself from the validation walk
// and makes it proceed directly to the value.
func (f Field[T]) skipTo() reflect.Value {
	return reflect.ValueOf(f.Value)
}
