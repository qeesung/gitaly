package gitalyauth

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHmacTokenValid(t *testing.T) {
	targetTime := time.Now()
	targetTimeStr := strconv.FormatInt(targetTime.Unix(), 10)
	secret := []byte("foo")
	timestampThreshold := 2 * time.Second

	testCases := []struct {
		desc   string
		token  string
		result bool
	}{
		{
			desc:   "Valid secret, time within threshold",
			token:  hmacToken("v2", secret, targetTime.Add(1*time.Second)),
			result: true,
		},
		{
			desc:   "Invalid secret, time within threshold",
			token:  hmacToken("v2", []byte("bar"), targetTime.Add(-1*time.Second)),
			result: false,
		},
		{
			desc:   "Valid secret, time outside threshold",
			token:  hmacToken("v2", secret, targetTime.Add(3*time.Second)),
			result: false,
		},
		{
			desc:   "Mismatching signed and clear message",
			token:  fmt.Sprintf("v2.%x.%d", hmacSign(secret, targetTimeStr), targetTime.Unix()-1),
			result: false,
		},
		{
			desc:   "Invalid version",
			token:  hmacToken("v3", secret, targetTime.Add(1*time.Second)),
			result: false,
		},
		{
			desc:   "Empty token",
			token:  "",
			result: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			result := hmacTokenValid([]byte(tc.token), secret, targetTime, timestampThreshold)

			require.Equal(t, tc.result, result)
		})
	}
}
