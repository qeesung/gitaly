package validator

import (
	"reflect"
	"strconv"
)

// Func is the type of a validation function that validates a value
// and returns an error if the validation fails.
type Func[T any] func(T) error

// Validator is an interface that provides validation functionality.
type Validator interface {
	// Validate the value and returns an error describing the validation
	// failure if any.
	Validate() error
}

// Validate validates the given value by invoking the value's Validate()
// method. If the value is a struct or a slice, each field and element are
// also recursed into and Validate() invoked on each field and element.
// If Validate() return an error, it is wrapped into an Error to annotate
// it with the field's or element's path in the hierarchy.
func Validate(value any) error {
	return validate("", reflect.ValueOf(value))
}

type skipper interface {
	// skipTo returns the field that the validation walk should consider next.
	// Useful for skipping some private fields from the walk.
	skipTo() reflect.Value
}

func validate(key string, value reflect.Value) error {
	var errs Errors
	if v, ok := value.Interface().(Validator); ok {
		if err := v.Validate(); err != nil {
			errs = errs.Append(err)
		}
	}

	if v, ok := value.Interface().(skipper); ok {
		value = v.skipTo()
	}

	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := validate(
				tomlName(value.Type().Field(i)),
				value.Field(i),
			); err != nil {
				errs = errs.Append(err)
			}
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := validate(
				strconv.FormatInt(int64(i), 10),
				value.Index(i),
			); err != nil {
				errs = errs.Append(err)
			}
		}
	}

	for i := range errs {
		errs[i] = NewError(key, errs[i])
	}

	return errs.ErrorOrNil()
}

func tomlName(value reflect.StructField) string {
	name, ok := value.Tag.Lookup("toml")
	if !ok {
		name = value.Name
	}

	return name
}
