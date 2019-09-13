package testhelper

import "sync"

// MockGauge is a simplified prometheus gauge with Inc and Dec that can be inspected
type MockGauge struct {
	sync.Mutex
	IncrementCalled, DecrementCalled int
}

// Inc increments the IncrementCalled counter
func (mg *MockGauge) Inc() {
	mg.Lock()
	defer mg.Unlock()
	mg.IncrementCalled++
}

// Dec increments the DecrementCalled counter
func (mg *MockGauge) Dec() {
	mg.Lock()
	defer mg.Unlock()
	mg.DecrementCalled++
}

// MockHistogram is a simplified prometheus histogram with Observe
type MockHistogram struct {
	sync.Mutex
	Values []float64
}

// Observe adds a value to the Values slice
func (mh *MockHistogram) Observe(d float64) {
	mh.Lock()
	defer mh.Unlock()
	mh.Values = append(mh.Values, d)
}
