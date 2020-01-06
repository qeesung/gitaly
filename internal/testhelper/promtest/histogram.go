package promtest

import (
	"sync"
)

// MockHistogram is a mock histogram that adheres to prometheus.Histogram for use in unit tests
type MockHistogram struct {
	m      sync.RWMutex
	Values []float64
}

// Observe observes a value for the mock histogram
func (m *MockHistogram) Observe(v float64) {
	m.m.Lock()
	defer m.m.Unlock()
	m.Values = append(m.Values, v)
}
