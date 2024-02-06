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
	ops.flush("removed/relative")

	testhelper.ProtoEqual(t, operations{
		{
			Operation: &gitalypb.LogEntry_Operation_CreateDirectory_{
				CreateDirectory: &gitalypb.LogEntry_Operation_CreateDirectory{
					Path:        "parent/child",
					Permissions: uint32(perm.PrivateDir),
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_CreateHardLink_{
				CreateHardLink: &gitalypb.LogEntry_Operation_CreateHardLink{
					SourcePath:      "path-in-log-entry",
					DestinationPath: "path-in-storage/1",
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_CreateHardLink_{
				CreateHardLink: &gitalypb.LogEntry_Operation_CreateHardLink{
					SourcePath:      "path-in-storage",
					SourceInStorage: true,
					DestinationPath: "path-in-storage/2",
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry_{
				RemoveDirectoryEntry: &gitalypb.LogEntry_Operation_RemoveDirectoryEntry{
					Path: "removed/relative/path",
				},
			},
		},
		{
			Operation: &gitalypb.LogEntry_Operation_Flush_{
				Flush: &gitalypb.LogEntry_Operation_Flush{
					Path: "removed/relative",
				},
			},
		},
	}, ops)
}
