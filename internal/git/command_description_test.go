//go:build !gitaly_test_sha256

package git

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandDescriptions_revListPositionalArgs(t *testing.T) {
	revlist, ok := commandDescriptions["rev-list"]
	require.True(t, ok)
	require.NotNil(t, revlist.validatePositionalArgs)

	for _, tc := range []struct {
		desc        string
		args        []string
		expectedErr error
	}{
		{
			desc: "normal reference",
			args: []string{
				"master",
			},
		},
		{
			desc: "reference with leading dash",
			args: []string{
				"-master",
			},
			expectedErr: fmt.Errorf("rev-list: %w",
				fmt.Errorf("positional arg \"-master\" cannot start with dash '-': %w", ErrInvalidArg),
			),
		},
		{
			desc: "revisions and pseudo-revisions",
			args: []string{
				"master --not --all",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			err := revlist.validatePositionalArgs(tc.args)
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestThreadsConfigValue(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		cpus    int
		threads string
	}{
		{1, "1"},
		{2, "1"},
		{3, "1"},
		{4, "2"},
		{8, "3"},
		{9, "3"},
		{13, "3"},
		{16, "4"},
		{27, "4"},
		{32, "5"},
	} {
		actualThreads := threadsConfigValue(tt.cpus)
		require.Equal(t, tt.threads, actualThreads)
	}
}

func TestFsckConfiguration_prefix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		prefix   string
		expected []ConfigPair
	}{
		{
			prefix: "fsck",
			expected: []ConfigPair{
				{Key: "transfer.fsckObjects", Value: "true"},
				{Key: "fsck.badTimezone", Value: "ignore"},
				{Key: "fsck.missingSpaceBeforeDate", Value: "ignore"},
				{Key: "fsck.zeroPaddedFilemode", Value: "ignore"},
			},
		},
		{
			prefix: "fetch.fsck",
			expected: []ConfigPair{
				{Key: "transfer.fsckObjects", Value: "true"},
				{Key: "fetch.fsck.badTimezone", Value: "ignore"},
				{Key: "fetch.fsck.missingSpaceBeforeDate", Value: "ignore"},
				{Key: "fetch.fsck.zeroPaddedFilemode", Value: "ignore"},
			},
		},
		{
			prefix: "receive.fsck",
			expected: []ConfigPair{
				{Key: "transfer.fsckObjects", Value: "true"},
				{Key: "receive.fsck.badTimezone", Value: "ignore"},
				{Key: "receive.fsck.missingSpaceBeforeDate", Value: "ignore"},
				{Key: "receive.fsck.zeroPaddedFilemode", Value: "ignore"},
			},
		},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			opts := templateFsckConfiguration(tc.prefix)

			for _, config := range tc.expected {
				require.Containsf(t, opts, config, fmt.Sprintf("missing %s", config.Key))
			}
		})
	}
}
