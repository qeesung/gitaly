package git

const (
	// EmptyTreeID is the Git tree object hash that corresponds to an empty tree (directory)
	EmptyTreeID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

	// NullSHA is the special value that Git uses to signal a ref or object does not exist
	NullSHA = "0000000000000000000000000000000000000000"

	// DisableAlternateRefsConfig is a config option for git-receive-pack. In
	// case the repository belongs to an object pool, we want to prevent Git
	// from including the pool's refs in the ref advertisement. We do this by
	// rigging core.alternateRefsCommand to produce no output. Because Git
	// itself will append the pool repository directory, the command ends
	// with a "#". The end result is that Git runs `/bin/sh -c 'exit 0 # /path/to/pool.git`.
	DisableAlternateRefsConfig = "core.alternateRefsCommand=exit 0 #"
)
