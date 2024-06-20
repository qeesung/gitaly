package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRaftIDMarshalBinary(t *testing.T) {
	testCases := []struct {
		name     string
		id       raftID
		expected []byte
	}{
		{
			name:     "zero",
			id:       0,
			expected: []byte{0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:     "one",
			id:       1,
			expected: []byte{0, 0, 0, 0, 0, 0, 0, 1},
		},
		{
			name:     "max",
			id:       raftID(^uint64(0)),
			expected: []byte{255, 255, 255, 255, 255, 255, 255, 255},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			binary := tc.id.MarshalBinary()

			require.Equal(t, tc.expected, binary)

			var id raftID
			id.UnmarshalBinary(binary)
			require.Equal(t, tc.id, id)
		})
	}
}

func TestRaftIDString(t *testing.T) {
	testCases := []struct {
		name     string
		id       raftID
		expected string
	}{
		{
			name:     "zero",
			id:       0,
			expected: "0",
		},
		{
			name:     "one",
			id:       1,
			expected: "1",
		},
		{
			name:     "max",
			id:       raftID(^uint64(0)),
			expected: "18446744073709551615",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, tc.id.String())
		})
	}
}

func TestRaftIDToUint64(t *testing.T) {
	testCases := []struct {
		name     string
		id       raftID
		expected uint64
	}{
		{
			name:     "zero",
			id:       0,
			expected: 0,
		},
		{
			name:     "one",
			id:       1,
			expected: 1,
		},
		{
			name:     "max",
			id:       raftID(^uint64(0)),
			expected: ^uint64(0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.id.ToUint64()
			if actual != tc.expected {
				t.Errorf("ToUint64(%d) = %d, expected %d", tc.id, actual, tc.expected)
			}
		})
	}
}
