package text

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// RandomHex returns an n-byte hexademical random string.
func RandomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}
