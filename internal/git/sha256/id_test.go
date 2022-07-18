package sha256

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateObjectID(t *testing.T) {
	for _, tc := range []struct {
		desc  string
		oid   string
		valid bool
	}{
		{
			desc:  "valid object ID",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92e",
			valid: true,
		},
		{
			desc:  "object ID with non-hex characters fails",
			oid:   "xff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92e",
			valid: false,
		},
		{
			desc:  "object ID with upper-case letters fails",
			oid:   "CFF96308C1B0C36602CA031013B563074EDDDB203EFA61C4F01BE1BD2482D92E",
			valid: false,
		},
		{
			desc:  "too short object ID fails",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92",
			valid: false,
		},
		{
			desc:  "too long object ID fails",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92eb",
			valid: false,
		},
		{
			desc:  "empty string fails",
			oid:   "",
			valid: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			err := ValidateObjectID(tc.oid)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.EqualError(t, err, fmt.Sprintf("invalid object ID: %q", tc.oid))
			}
		})
	}
}

func TestNewObjectIDFromHex(t *testing.T) {
	for _, tc := range []struct {
		desc  string
		oid   string
		valid bool
	}{
		{
			desc:  "valid object ID",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92e",
			valid: true,
		},
		{
			desc:  "object ID with non-hex characters fails",
			oid:   "xff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92e",
			valid: false,
		},
		{
			desc:  "object ID with upper-case letters fails",
			oid:   "CFF96308C1B0C36602CA031013B563074EDDDB203EFA61C4F01BE1BD2482D92E",
			valid: false,
		},
		{
			desc:  "too short object ID fails",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92",
			valid: false,
		},
		{
			desc:  "too long object ID fails",
			oid:   "cff96308c1b0c36602ca031013b563074edddb203efa61c4f01be1bd2482d92eb",
			valid: false,
		},
		{
			desc:  "empty string fails",
			oid:   "",
			valid: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid, err := NewObjectIDFromHex(tc.oid)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, tc.oid, oid.String())
			} else {
				require.Error(t, err)
			}
		})
	}
}
