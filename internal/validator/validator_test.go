package validator_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/validator"
)

type CustomString string

// Custom types can have validators attached to them. Their path in the
// struct's hierarchy is automatically annotated on the errors.
func (str CustomString) Validate() error {
	// The validator functions are composable.
	return validator.Combine(
		validator.IsSet[CustomString],
		validator.Equal[CustomString]("success"),
	)(str)
}

type StringEnum string

func (enum StringEnum) Validate() error {
	return validator.IsOneOf("good-1", "good-2", "good-3")(string(enum))
}

type ChildStruct struct {
	ShouldFail   bool
	EnumValue    StringEnum   `toml:"enum_value"`
	CustomString CustomString `toml:"custom_string"`
	// Fields which don't have validation logic are ignored.
	UnvalidatedString string `toml:"unvalidated_string"`
}

// Validate functons are called on field that implements one, even in subfields.
func (c ChildStruct) Validate() error {
	if c.ShouldFail {
		// Returning a validator.Error allows for annotating the returned error with a
		// key so the correct location in the struct can be reported even if a subfield
		// was being validated from a parent struct level validator.
		return validator.NewError("should_fail", errors.New("struct validation error"))
	}

	return nil
}

type StringSlice []CustomString

func (slc StringSlice) Validate() error {
	if len(slc) == 0 {
		return errors.New("must have elements")
	}

	return nil
}

type Configuration struct {
	ShouldFail   bool
	StringSlice  StringSlice  `toml:"string_slice"`
	CustomString CustomString `toml:"custom_string"`
	// The ChildStruct gets walked into as well and validation invoked for each
	// field separately.
	ChildStruct     ChildStruct                  `toml:"child_struct"`
	ValidatedInt    validator.Field[int]         `toml:"validated_int"`
	ValidatedStruct validator.Field[ChildStruct] `toml:"validated_struct"`
}

// Validate functons are called on field that implements one, even on the root level.
func (c Configuration) Validate() error {
	if c.ShouldFail {
		// Returned errors don't need to have a special type.
		return errors.New("struct validation error")
	}

	return nil
}

func newChildStruct() ChildStruct {
	return ChildStruct{
		EnumValue:         "good-1",
		CustomString:      "success",
		UnvalidatedString: "default_unvalidated",
	}
}

func NewConfiguration() Configuration {
	return Configuration{
		CustomString: "success",
		StringSlice:  StringSlice{"success", "success", "success"},
		ChildStruct:  newChildStruct(),
		ValidatedInt: validator.NewField(12, validator.IsInRange(10, 15)),
		ValidatedStruct: validator.NewField(newChildStruct(), func(value ChildStruct) error {
			if value != newChildStruct() {
				return errors.New("struct must not be changed")
			}

			return nil
		}),
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc        string
		cfg         func(cfg *Configuration)
		expectedErr string
	}{
		{
			desc: "valid configuration",
			cfg:  func(*Configuration) {},
		},
		{
			desc: "invalid top level value",
			cfg: func(cfg *Configuration) {
				cfg.CustomString = ""
			},
			expectedErr: "custom_string: must be set",
		},
		{
			desc: "invalid child value",
			cfg: func(cfg *Configuration) {
				cfg.ChildStruct.CustomString = ""
			},
			expectedErr: "child_struct.custom_string: must be set",
		},
		{
			desc: "multiple invalid",
			cfg: func(cfg *Configuration) {
				cfg.CustomString = ""
				cfg.ChildStruct.CustomString = "fail"
			},
			expectedErr: `custom_string: must be set
child_struct.custom_string: must equal "success"`,
		},
		{
			desc: "field not in range",
			cfg: func(cfg *Configuration) {
				cfg.ValidatedInt.Value = 2
			},
			expectedErr: "validated_int: must be in range [10, 15]",
		},
		{
			desc: "value equals min of the range",
			cfg: func(cfg *Configuration) {
				cfg.ValidatedInt.Value = 10
			},
		},
		{
			desc: "value equals max of the range",
			cfg: func(cfg *Configuration) {
				cfg.ValidatedInt.Value = 15
			},
		},
		{
			desc: "struct validation",
			cfg: func(cfg *Configuration) {
				cfg.ShouldFail = true
			},
			expectedErr: "struct validation error",
		},
		{
			desc: "child struct validation",
			cfg: func(cfg *Configuration) {
				cfg.ChildStruct.ShouldFail = true
			},
			expectedErr: "child_struct.should_fail: struct validation error",
		},
		{
			desc: "field struct validation",
			cfg: func(cfg *Configuration) {
				cfg.ValidatedStruct.Value.EnumValue = "bad-1"
			},
			expectedErr: `validated_struct: struct must not be changed
validated_struct.enum_value: must be one of [good-1 good-2 good-3]`,
		},
		{
			desc: "slice validation",
			cfg: func(cfg *Configuration) {
				cfg.StringSlice = nil
			},
			expectedErr: `string_slice: must have elements`,
		},
		{
			desc: "invalid elements in slice",
			cfg: func(cfg *Configuration) {
				cfg.StringSlice = StringSlice{"", "success", "fail"}
			},
			expectedErr: `string_slice.0: must be set
string_slice.2: must equal "success"`,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := NewConfiguration()
			tc.cfg(&cfg)

			if err := validator.Validate(cfg); tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
		})
	}
}
