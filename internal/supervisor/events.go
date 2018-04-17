package supervisor

// EventType is used to label Event instances.
type EventType int

const (
	// Up is a notification that the process with the accompanying PID is up.
	Up EventType = iota
	// MemoryHigh is a notification that process memory for the current PID
	// exceeds the threshold.
	MemoryHigh
	// MemoryLow indicates the process memory is at or below the threshold.
	MemoryLow
	HealthGood
	HealthBad
)

// Event is used to notify a listener of process state changes.
type Event struct {
	Type  EventType
	Pid   int
	Error error
}
