//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
)

func TestCheckSubcommand_Exec(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc                string
		checks              []service.CheckFunc
		expectedQuietOutput string
		expectedOutput      string
		expectedError       error
	}{
		{
			desc: "all checks pass",
			checks: []service.CheckFunc{
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 1",
						Description: "checks a",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 2",
						Description: "checks b",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 3",
						Description: "checks c",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
			},
			expectedOutput: `Checking check 1 - checks a [fatal]
Passed
Checking check 2 - checks b [fatal]
Passed
Checking check 3 - checks c [fatal]
Passed

All checks passed.
`,
			expectedQuietOutput: `Checking check 1...Passed
Checking check 2...Passed
Checking check 3...Passed

All checks passed.
`,
			expectedError: nil,
		},
		{
			desc: "a fatal check fails",
			checks: []service.CheckFunc{
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 1",
						Description: "checks a",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 2",
						Description: "checks b",
						Run:         func(ctx context.Context) error { return errors.New("i failed") },
						Severity:    service.Fatal,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 3",
						Description: "checks c",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
			},
			expectedOutput: `Checking check 1 - checks a [fatal]
Passed
Checking check 2 - checks b [fatal]
Failed (fatal) error: i failed
Checking check 3 - checks c [fatal]
Passed

1 check(s) failed, at least one was fatal.
`,
			expectedQuietOutput: `Checking check 1...Passed
Checking check 2...Failed (fatal) error: i failed
Checking check 3...Passed

1 check(s) failed, at least one was fatal.
`,
			expectedError: errFatalChecksFailed,
		},
		{
			desc: "only warning checks fail",
			checks: []service.CheckFunc{
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 1",
						Description: "checks a",
						Run:         func(ctx context.Context) error { return nil },
						Severity:    service.Fatal,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 2",
						Description: "checks b",
						Run:         func(ctx context.Context) error { return errors.New("i failed but not too badly") },
						Severity:    service.Warning,
					}
				},
				func(cfg config.Config, w io.Writer, quiet bool) *service.Check {
					return &service.Check{
						Name:        "check 3",
						Description: "checks c",
						Run:         func(ctx context.Context) error { return errors.New("i failed but not too badly") },
						Severity:    service.Warning,
					}
				},
			},
			expectedOutput: `Checking check 1 - checks a [fatal]
Passed
Checking check 2 - checks b [warning]
Failed (warning) error: i failed but not too badly
Checking check 3 - checks c [warning]
Failed (warning) error: i failed but not too badly

2 check(s) failed, but none are fatal.
`,
			expectedQuietOutput: `Checking check 1...Passed
Checking check 2...Failed (warning) error: i failed but not too badly
Checking check 3...Failed (warning) error: i failed but not too badly

2 check(s) failed, but none are fatal.
`,
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			var cfg config.Config

			t.Run("quiet", func(t *testing.T) {
				var stdout bytes.Buffer
				checkCmd := checkSubcommand{w: &stdout, checkFuncs: tc.checks, quiet: true}
				assert.Equal(t, tc.expectedError, checkCmd.Exec(flag.NewFlagSet("", flag.PanicOnError), cfg))
				assert.Equal(t, tc.expectedQuietOutput, stdout.String())
			})

			t.Run("normal", func(t *testing.T) {
				var stdout bytes.Buffer
				checkCmd := checkSubcommand{w: &stdout, checkFuncs: tc.checks, quiet: false}
				assert.Equal(t, tc.expectedError, checkCmd.Exec(flag.NewFlagSet("", flag.PanicOnError), cfg))
				assert.Equal(t, tc.expectedOutput, stdout.String())
			})
		})
	}
}
