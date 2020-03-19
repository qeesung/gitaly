package slice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUint64_Minus(t *testing.T) {
	testCases := []struct {
		desc  string
		left  Uint64
		right Uint64
		exp   Uint64
	}{
		{desc: "empty left", left: Uint64{}, right: Uint64{1, 2}, exp: nil},
		{desc: "empty right", left: Uint64{1, 2}, right: Uint64{}, exp: Uint64{1, 2}},
		{desc: "some exists", left: Uint64{1, 2, 3, 4, 5}, right: Uint64{2, 4, 5}, exp: Uint64{1, 3}},
		{desc: "nothing exists", left: Uint64{10, 20}, right: Uint64{100, 200}, exp: Uint64{10, 20}},
		{desc: "duplicates exists", left: Uint64{1, 1, 2, 3, 3, 4, 4, 5}, right: Uint64{3, 4, 4, 5}, exp: Uint64{1, 1, 2}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			require.Equal(t, testCase.exp, testCase.left.Minus(testCase.right))
		})
	}
}
