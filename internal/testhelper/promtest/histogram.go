package promtest

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
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

func NewMockHistogramVec() *MockHistogramVec {
	return &MockHistogramVec{}
}

type MockHistogramVec struct {
	m            sync.RWMutex
	labelsCalled [][]string
	observer     MockObserver
}

func (m *MockHistogramVec) LabelsCalled() [][]string {
	m.m.RLock()
	defer m.m.RUnlock()

	return m.labelsCalled
}

func (m *MockHistogramVec) Observer() *MockObserver {
	return &m.observer
}

func (m *MockHistogramVec) WithLabelValues(lvs ...string) prometheus.Observer {
	m.m.Lock()
	defer m.m.Unlock()

	m.labelsCalled = append(m.labelsCalled, lvs)
	return &m.observer
}

type MockObserver struct {
	m        sync.RWMutex
	observed []float64
}

func (m *MockObserver) Observe(v float64) {
	m.m.Lock()
	defer m.m.Unlock()

	m.observed = append(m.observed, v)
}

func (m *MockObserver) Observed() []float64 {
	m.m.RLock()
	defer m.m.RUnlock()

	return m.observed
}
