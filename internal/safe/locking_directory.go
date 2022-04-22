package safe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type lockingDirectoryState int

const (
	lockingDirectoryStateUnlocked lockingDirectoryState = iota
	lockingDirectoryStateLocked
)

// LockingDirectory allows locking and unlocking a directory for safe access and
// modification.
type LockingDirectory struct {
	state lockingDirectoryState
	path  string
}

// NewLockingDirectory creates a new LockingDirectory.
func NewLockingDirectory(path string) (*LockingDirectory, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("creating new locking directory: %w", err)
	}

	if !fi.IsDir() {
		return nil, errors.New("not a directory")
	}

	ld := &LockingDirectory{
		state: lockingDirectoryStateUnlocked,
		path:  path,
	}

	return ld, nil
}

// Lock locks the directory and prevents a second process with a
// LockingDirectory from also locking the directory.
func (ld *LockingDirectory) Lock() error {
	if ld.state != lockingDirectoryStateUnlocked {
		return errors.New("locking directory not lockable")
	}

	lock, err := os.OpenFile(ld.lockPath(), os.O_CREATE|os.O_EXCL|os.O_RDONLY, 0o400)
	if err != nil {
		if os.IsExist(err) {
			return ErrFileAlreadyLocked
		}

		return fmt.Errorf("creating lock file: %w", err)
	}
	_ = lock.Close()

	ld.state = lockingDirectoryStateLocked

	return nil
}

// IsLocked returns whether or not the directory has been locked.
func (ld *LockingDirectory) IsLocked() bool {
	return ld.state == lockingDirectoryStateLocked
}

// Unlock unlocks the directory.
func (ld *LockingDirectory) Unlock() error {
	if ld.state != lockingDirectoryStateLocked {
		return errors.New("locking directory not locked")
	}

	if err := os.Remove(ld.lockPath()); err != nil {
		// A previous call might have returned an error
		// but still removed the file.
		if errors.Is(err, fs.ErrNotExist) {
			ld.state = lockingDirectoryStateUnlocked
			return nil
		}

		return fmt.Errorf("unlocking directory: %w", err)
	}

	ld.state = lockingDirectoryStateUnlocked

	return nil
}

func (ld *LockingDirectory) lockPath() string {
	return filepath.Join(ld.path, ".lock")
}
