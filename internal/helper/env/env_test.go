package env_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestGetBool(t *testing.T) {
	const envvar = "TEST_BOOL"

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T)
		fallback      bool
		expectedValue bool
		expectedErr   error
	}{
		{
			desc:          "explicitly true",
			setup:         setupEnv(envvar, "true"),
			expectedValue: true,
		},
		{
			desc:          "explicitly false",
			setup:         setupEnv(envvar, "false"),
			expectedValue: false,
		},
		{
			desc:          "explicitly 1",
			setup:         setupEnv(envvar, "1"),
			expectedValue: true,
		},
		{
			desc:          "explicitly 0",
			setup:         setupEnv(envvar, "0"),
			expectedValue: false,
		},
		{
			desc: "missing value",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			expectedValue: false,
		},
		{
			desc: "missing value with fallback",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			fallback:      true,
			expectedValue: true,
		},
		{
			desc:          "empty value",
			setup:         setupEnv(envvar, ""),
			expectedValue: false,
		},
		{
			desc:          "empty value with fallback",
			setup:         setupEnv(envvar, ""),
			fallback:      true,
			expectedValue: true,
		},
		{
			desc:          "invalid value",
			setup:         setupEnv(envvar, "bad"),
			expectedValue: false,
			expectedErr: fmt.Errorf("get bool TEST_BOOL: %w", &strconv.NumError{
				Func: "ParseBool",
				Num:  "bad",
				Err:  strconv.ErrSyntax,
			}),
		},
		{
			desc:          "invalid value with fallback",
			setup:         setupEnv(envvar, "bad"),
			fallback:      true,
			expectedValue: true,
			expectedErr: fmt.Errorf("get bool TEST_BOOL: %w", &strconv.NumError{
				Func: "ParseBool",
				Num:  "bad",
				Err:  strconv.ErrSyntax,
			}),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.setup(t)

			value, err := env.GetBool(envvar, tc.fallback)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedValue, value)
		})
	}
}

func TestGetInt(t *testing.T) {
	const envvar = "TEST_INT"

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T)
		fallback      int
		expectedValue int
		expectedErr   error
	}{
		{
			desc:          "valid number",
			setup:         setupEnv(envvar, "3"),
			expectedValue: 3,
		},
		{
			desc: "unset value",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			expectedValue: 0,
		},
		{
			desc: "unset value with fallback",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			fallback:      3,
			expectedValue: 3,
		},
		{
			desc:          "empty value",
			setup:         setupEnv(envvar, ""),
			expectedValue: 0,
		},
		{
			desc:          "empty value with fallback",
			setup:         setupEnv(envvar, ""),
			fallback:      3,
			expectedValue: 3,
		},
		{
			desc:          "invalid value",
			setup:         setupEnv(envvar, "bad"),
			expectedValue: 0,
			expectedErr: fmt.Errorf("get int TEST_INT: %w", &strconv.NumError{
				Func: "Atoi",
				Num:  "bad",
				Err:  strconv.ErrSyntax,
			}),
		},
		{
			desc:          "invalid value with fallback",
			setup:         setupEnv(envvar, "bad"),
			fallback:      3,
			expectedValue: 3,
			expectedErr: fmt.Errorf("get int TEST_INT: %w", &strconv.NumError{
				Func: "Atoi",
				Num:  "bad",
				Err:  strconv.ErrSyntax,
			}),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.setup(t)

			value, err := env.GetInt(envvar, tc.fallback)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedValue, value)
		})
	}
}

func TestGetDuration(t *testing.T) {
	const envvar = "TEST_DURATION"

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T)
		fallback      time.Duration
		expectedValue time.Duration
		expectedErr   error
	}{
		{
			desc:          "valid duration",
			setup:         setupEnv(envvar, "3m"),
			fallback:      0,
			expectedValue: 3 * time.Minute,
		},
		{
			desc: "unset value",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			expectedValue: 0,
		},
		{
			desc: "unset value with fallback",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			fallback:      3,
			expectedValue: 3,
		},
		{
			desc:          "empty value",
			setup:         setupEnv(envvar, ""),
			expectedValue: 0,
		},
		{
			desc:          "empty value with fallback",
			setup:         setupEnv(envvar, ""),
			fallback:      3,
			expectedValue: 3,
		},
		{
			desc:          "invalid value",
			setup:         setupEnv(envvar, "bad"),
			expectedValue: 0,
			expectedErr:   fmt.Errorf("get duration TEST_DURATION: %w", fmt.Errorf("time: invalid duration \"bad\"")),
		},
		{
			desc:          "invalid value with fallback",
			setup:         setupEnv(envvar, "bad"),
			fallback:      3,
			expectedValue: 3,
			expectedErr:   fmt.Errorf("get duration TEST_DURATION: %w", fmt.Errorf("time: invalid duration \"bad\"")),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.setup(t)

			value, err := env.GetDuration(envvar, tc.fallback)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedValue, value)
		})
	}
}

func TestGetString(t *testing.T) {
	const envvar = "TEST_STRING"

	for _, tc := range []struct {
		desc          string
		setup         func(t *testing.T)
		fallback      string
		expectedValue string
	}{
		{
			desc:          "simple string",
			setup:         setupEnv(envvar, "Hello"),
			expectedValue: "Hello",
		},
		{
			desc:          "string with trailing whitespace",
			setup:         setupEnv(envvar, "hello "),
			expectedValue: "hello",
		},
		{
			desc: "unset value",
			setup: func(t *testing.T) {
				testhelper.Unsetenv(t, envvar)
			},
			fallback:      "fallback value",
			expectedValue: "fallback value",
		},
		{
			desc:          "empty value",
			setup:         setupEnv(envvar, ""),
			fallback:      "fallback value",
			expectedValue: "fallback value",
		},
		{
			desc:          "whitespace only",
			setup:         setupEnv(envvar, " "),
			fallback:      "fallback value",
			expectedValue: "",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.setup(t)

			value := env.GetString(envvar, tc.fallback)
			require.Equal(t, tc.expectedValue, value)
		})
	}
}

func TestExtractKey(t *testing.T) {
	for _, tc := range []struct {
		desc          string
		environment   []string
		key           string
		expectedValue string
	}{
		{
			desc:          "nil",
			environment:   nil,
			key:           "something",
			expectedValue: "",
		},
		{
			desc: "found",
			environment: []string{
				"FOO=bar",
				"BAR=qux",
			},
			key:           "BAR",
			expectedValue: "qux",
		},
		{
			desc: "found with multiple matches",
			environment: []string{
				"FOO=1",
				"FOO=2",
			},
			key:           "FOO",
			expectedValue: "2",
		},
		{
			desc: "not found",
			environment: []string{
				"FOO=bar",
				"BAR=qux",
			},
			key:           "doesnotexist",
			expectedValue: "",
		},
		{
			desc: "prefix-match",
			environment: []string{
				"FOObar=value",
			},
			key:           "FOO",
			expectedValue: "",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			value := env.ExtractValue(tc.environment, tc.key)
			require.Equal(t, tc.expectedValue, value)
		})
	}
}

func setupEnv(key, value string) func(t *testing.T) {
	return func(t *testing.T) {
		t.Setenv(key, value)
	}
}
