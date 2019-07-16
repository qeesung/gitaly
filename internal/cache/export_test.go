package cache

import "sync"

var (
	ExportMockRemovalCounter = new(mockCounter)
	ExportMockCheckCounter   = new(mockCounter)
)

type mockCounter struct {
	sync.RWMutex
	count int
}

func (mc *mockCounter) Add(n int) {
	mc.Lock()
	mc.count += n
	mc.Unlock()
}

func (mc *mockCounter) Count() int {
	mc.RLock()
	defer mc.RUnlock()
	return mc.count
}

func init() {
	// override counter functions with our mocked version
	countWalkRemoval = func() { ExportMockRemovalCounter.Add(1) }
	countWalkCheck = func() { ExportMockCheckCounter.Add(1) }
}
