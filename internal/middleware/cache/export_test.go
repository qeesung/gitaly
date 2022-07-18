//go:build !gitaly_test_sha256

package cache

import "sync"

var MethodErrCount = struct {
	sync.Mutex
	Method map[string]int
}{
	Method: map[string]int{},
}

func init() {
	// override prometheus counter to detect any errors logged for a specific
	// method
	countMethodErr = func(method string) {
		MethodErrCount.Lock()
		MethodErrCount.Method[method]++
		MethodErrCount.Unlock()
	}
}
