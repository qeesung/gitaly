package protoregistry

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOID(t *testing.T) {
	for _, tt := range []struct {
		raw       string
		expectOID []int
		expectErr error
	}{
		{
			raw: "",
		},
		{
			raw:       "1",
			expectOID: []int{1},
		},
		{
			raw:       "1.1",
			expectOID: []int{1, 1},
		},
		{
			raw:       "1.2.1",
			expectOID: []int{1, 2, 1},
		},
		{
			raw:       "a.b.c",
			expectErr: errors.New("unable to parse target field OID a.b.c: strconv.Atoi: parsing \"a\": invalid syntax"),
		},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			actualOID, actualErr := parseOID(tt.raw)
			require.Equal(t, tt.expectOID, actualOID)
			require.Equal(t, tt.expectErr, actualErr)
		})
	}
}
