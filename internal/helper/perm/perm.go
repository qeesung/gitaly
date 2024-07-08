// Package perm provides constants for file and directory permissions.
//
// Note that these permissions are further restricted by the system configured
// umask.
package perm

import (
	"io/fs"
)

const (
	// PrivateDir is the permissions given for a directory that must only be
	// used by gitaly.
	PrivateDir fs.FileMode = 0o700

	// PrivateWriteOnceFile is the most restrictive file permission. Given to
	// files that are expected to be written only once and must be read only by
	// gitaly.
	PrivateWriteOnceFile fs.FileMode = 0o400

	// SharedFile is the permission given for a file that may be read outside
	// of gitaly.
	SharedFile fs.FileMode = 0o644

	// PrivateExecutable is the permissions given for an executable that must
	// only be used by gitaly.
	PrivateExecutable fs.FileMode = 0o700

	// SharedExecutable is the permission given for an executable that may be
	// executed outside of gitaly.
	SharedExecutable fs.FileMode = 0o755
)

// Umask represents a umask that is used to mask mode bits.
type Umask int

// Mask applies the mask on the mode.
func (mask Umask) Mask(mode fs.FileMode) fs.FileMode {
	return mode & ^fs.FileMode(mask)
}
