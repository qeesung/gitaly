package storage

import "io/fs"

const (
	// ModeDirectory is the mode directories are stored with in the storage.
	// It gives the owner read, write, and execute permissions on directories.
	ModeDirectory = fs.ModeDir | 0o700
)
