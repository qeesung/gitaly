package storage

import "io/fs"

const (
	// ModeDirectory is the mode directories are stored with in the storage.
	// It gives the owner read, write, and execute permissions on directories.
	ModeDirectory fs.FileMode = fs.ModeDir | 0o700
	// ModeReadOnlyDirectory is the mode given to directories in read-only snapshots.
	// It gives the owner read and execute permissions on directories.
	ModeReadOnlyDirectory fs.FileMode = fs.ModeDir | 0o500
	// ModeExecutable is the mode executable files are stored with in the storage.
	// It gives the owner read and execute permissions on the executable files.
	ModeExecutable fs.FileMode = 0o500
	// ModeFile is the mode files are stored with in the storage.
	// It gives the owner read permissions on the files.
	ModeFile fs.FileMode = 0o400
)
