package cache

import "sync"

var (
	ExportMockRemovalCounter = &mockCounter{}
	ExportMockCheckCounter   = &mockCounter{}
	ExportMockLoserBytes     = &mockCounter{}

	ExportDisableMoveAndClear = &disableMoveAndClear
	ExportDisableWalker       = &disableWalker
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
	countLoserBytes = func(n float64) { ExportMockLoserBytes.Add(int(n)) }
}
