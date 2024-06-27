package snapshot

// FileSystem is a snapshot of a file system's state.
type FileSystem interface {
	// Root returns the root of the snapshot's file system.
	Root() string
	// Prefix returns the prefix of the snapshot within the original root file system.
	Prefix() string
	// RelativePath returns the given relative path in the original file system rewritten to
	// point to the relative path in the snapshot.
	RelativePath(relativePath string) string
	// Closes closes the file system and releases resources associated with it.
	Close() error
}
