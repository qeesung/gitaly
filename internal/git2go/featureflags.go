package git2go

// FeatureFlags is a struct only used by tests to confirm that feature flags are
// being properly propagated from the git2go Executor to the gitaly-git2go
// binary
type FeatureFlags struct {
	// Raw is a map of feature flags and their corresponding values
	Raw map[string]string
	// Err is set if an error occurred. Err must exist on all gob serialized
	// results so that any error can be returned.
	Err error
}
