package git2go

// FeatureFlag is a feature flag state as seen by the `gitaly-git2go featureflag` test subcommand.
type FeatureFlag struct {
	// MetadataKey is the metadata key of the feature flag.
	MetadataKey string
	// Name is the name of the feature flag.
	Name string
	// Value is the value of the feature flag.
	Value bool
}

// FeatureFlags is a struct only used by tests to confirm that feature flags are
// being properly propagated from the git2go Executor to the gitaly-git2go
// binary
type FeatureFlags struct {
	// Flags is the set of feature flags observed by the command.
	Flags []FeatureFlag
	// Err is set if an error occurred. Err must exist on all gob serialized
	// results so that any error can be returned.
	Err error
}
