package wal

import (
	"io/fs"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// operations is a helper type around []*gitalypb.LogEntry_Operation.
type operations []*gitalypb.LogEntry_Operation

// append appends an operation to the operations.
func (ops *operations) append(op *gitalypb.LogEntry_Operation) {
	*ops = append(*ops, op)
}

// createDirectory appends an operations that creates a directory in the storage
// at the given relative path with the given permissions.
func (ops *operations) createDirectory(relativePath string, permissions fs.FileMode) {
	ops.append(&gitalypb.LogEntry_Operation{
		Operation: &gitalypb.LogEntry_Operation_CreateDirectory_{
			CreateDirectory: &gitalypb.LogEntry_Operation_CreateDirectory{
				Path:        []byte(relativePath),
				Permissions: uint32(permissions),
			},
		},
	})
}

// createHardLink appends a hard linking operations. The file at source path is hard linked to the
// destination path. By default, the source is considered to be relative to the log entry which is used to
// link logged files into the storage. If sourceInStorage is set, the source path is considered to be relative
// to the storage, which is used to link existing files in the storage to new locations.
func (ops *operations) createHardLink(sourceRelativePath, destinationRelativePath string, sourceInStorage bool) {
	ops.append(&gitalypb.LogEntry_Operation{
		Operation: &gitalypb.LogEntry_Operation_CreateHardLink_{
			CreateHardLink: &gitalypb.LogEntry_Operation_CreateHardLink{
				SourcePath:      []byte(sourceRelativePath),
				SourceInStorage: sourceInStorage,
				DestinationPath: []byte(destinationRelativePath),
			},
		},
	})
}

// removeDirectoryEntry appends an operation to remove a given directory entry from the storage.
// If the target is a directory, it must be empty.
func (ops *operations) removeDirectoryEntry(relativePath string) {
	ops.append(&gitalypb.LogEntry_Operation{
		Operation: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry_{
			RemoveDirectoryEntry: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry{
				Path: []byte(relativePath),
			},
		},
	})
}
