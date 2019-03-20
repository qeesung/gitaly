package text

import "strings"

// ChompBytes converts b to a string with its trailing newline, if present, removed.
func ChompBytes(b []byte) string {
	return strings.TrimSuffix(string(b), "\n")
}
