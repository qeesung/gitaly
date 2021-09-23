package helper

import (
	"bytes"
	"sync"
)

// ByteSliceHasAnyPrefix tests whether the byte slice s begins with any of the prefixes.
func ByteSliceHasAnyPrefix(s []byte, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if bytes.HasPrefix(s, []byte(prefix)) {
			return true
		}
	}

	return false
}

// IsNumber tests whether the byte slice s contains only digits or not
func IsNumber(s []byte) bool {
	for i := range s {
		if !bytes.Contains([]byte("1234567890"), s[i:i+1]) {
			return false
		}
	}
	return true
}

// UnquoteBytes removes surrounding double-quotes from a byte slice returning
// a new slice if they exist, otherwise it returns the same byte slice passed.
func UnquoteBytes(s []byte) []byte {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}

	return s
}

// bytesBufferPool is a shared pool of the *bytes.Buffer instances for re-use.
var bytesBufferPool = sync.Pool{New: func() interface{} {
	return &bytes.Buffer{}
}}

// Buffer returns an instance of the *bytes.Buffer and a release function.
// The release function should be called once the instance is not needed anymore.
// It will allow for instance to be re-used without re-creation of it.
// NOTE: be careful with usage of the bytes.Buffer.Bytes method as the underling
// bytes buffer will be reset once release function called. If you need to reference
// it later you need to copy it explicitly.
func Buffer() (*bytes.Buffer, func()) {
	buf := bytesBufferPool.Get().(*bytes.Buffer)
	return buf, func() {
		buf.Reset()
		bytesBufferPool.Put(buf)
	}
}
