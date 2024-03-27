package config

import "time"

// WriteCommitGraphConfig contains configuration that can be passed to WriteCommitGraph to alter its
// default behaviour.
type WriteCommitGraphConfig struct {
	// ReplaceChain causes WriteCommitGraph to rewrite the complete commit-graph chain. This is
	// a lot more expensive than the default, incremental update of the commit-graph chains but
	// may be required in certain cases to fix up commit-graphs.
	ReplaceChain bool
}

// RepackObjectsStrategy defines how objects shall be repacked.
type RepackObjectsStrategy string

const (
	// RepackObjectsStrategyIncrementalWithUnreachable performs an incremental repack by writing
	// all loose objects into a new packfile, regardless of their reachability. The loose
	// objects will be deleted.
	RepackObjectsStrategyIncrementalWithUnreachable = RepackObjectsStrategy("incremental_with_unreachable")
	// RepackObjectsStrategyFullWithCruft performs a full repack by writing all reachable
	// objects into a new packfile. Unreachable objects will be written into a separate cruft
	// packfile.
	RepackObjectsStrategyFullWithCruft = RepackObjectsStrategy("full_with_cruft")
	// RepackObjectsStrategyFullWithUnreachable performs a full repack by writing all reachable
	// objects into a new packfile. Packed unreachable objects will be appended to the packfile
	// and redundant loose object files will be deleted.
	//
	// Note that this will not include unreachable loose objects, but only packed loose objects.
	// git-repack(1) does not currently expose an option to make it include all objects.
	// Combined with geometric repacks though this is acceptable as the geometric strategy will
	// include all loose objects.
	RepackObjectsStrategyFullWithUnreachable = RepackObjectsStrategy("full_with_unreachable")
	// RepackObjectsStrategyGeometric performs an geometric repack. This strategy will repack
	// packfiles so that the resulting pack structure forms a geometric sequence in the number
	// of objects. Loose objects will get soaked up as part of the repack regardless of their
	// reachability.
	RepackObjectsStrategyGeometric = RepackObjectsStrategy("geometric")
)

// RepackObjectsConfig is configuration for RepackObjects.
type RepackObjectsConfig struct {
	// Strategy determines the strategy with which to repack objects.
	Strategy RepackObjectsStrategy
	// WriteBitmap determines whether reachability bitmaps should be written or not. There is no
	// reason to set this to `false`, except for legacy compatibility reasons with existing RPC
	// behaviour
	WriteBitmap bool
	// WriteMultiPackIndex determines whether a multi-pack index should be written or not.
	WriteMultiPackIndex bool
	// CruftExpireBefore determines the cutoff date before which unreachable cruft objects shall
	// be expired and thus deleted.
	CruftExpireBefore time.Time
}
