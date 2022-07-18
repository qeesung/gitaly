//go:build !gitaly_test_sha256

package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChompBytes(t *testing.T) {
	testCases := []struct {
		desc string
		in   []byte
		out  string
	}{
		{desc: "no space, trailing newline", in: []byte("hello world\n"), out: "hello world"},
		{desc: "space, trailing newline", in: []byte(" hello world \n"), out: " hello world "},
		{desc: "no space, no trailing newline", in: []byte("hello world"), out: "hello world"},
		{desc: "space, no trailing newline", in: []byte(" hello world "), out: " hello world "},
		{desc: "double newline", in: []byte(" hello world \n\n"), out: " hello world \n"},
		{desc: "empty slice", in: []byte{}, out: ""},
		{desc: "nil slice", in: nil, out: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.out, ChompBytes(tc.in))
		})
	}
}
