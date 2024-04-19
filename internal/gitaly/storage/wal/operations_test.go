package wal

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestOperations(t *testing.T) {
	var ops operations

	ops.createDirectory("parent/child", perm.PrivateDir)
	ops.createHardLink("path-in-log-entry", "path-in-storage/1", false)
	ops.createHardLink("path-in-storage", "path-in-storage/2", true)
	ops.removeDirectoryEntry("removed/relative/path")

	testhelper.ProtoEqual(t, operations{
		{
			Operation: &gitalypb.LogEntry_Operation_CreateDirectory_{
				CreateDirectory: &gitalypb.LogEntry_Operation_CreateDirectory{
					Path:        []byte("parent/child"),
					Permissions: uint32(perm.PrivateDir),
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_CreateHardLink_{
				CreateHardLink: &gitalypb.LogEntry_Operation_CreateHardLink{
					SourcePath:      []byte("path-in-log-entry"),
					DestinationPath: []byte("path-in-storage/1"),
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_CreateHardLink_{
				CreateHardLink: &gitalypb.LogEntry_Operation_CreateHardLink{
					SourcePath:      []byte("path-in-storage"),
					SourceInStorage: true,
					DestinationPath: []byte("path-in-storage/2"),
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry_{
				RemoveDirectoryEntry: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry{
					Path: []byte("removed/relative/path"),
				},
			},
		},
	}, ops)
}
