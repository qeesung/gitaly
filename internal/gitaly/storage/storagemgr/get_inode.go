package storagemgr

import (
	"fmt"
	"os"
	"syscall"
)

// getInode gets the inode of a file system object at the given path.
func getInode(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected stat type: %w", err)
	}

	return stat.Ino, nil
}
