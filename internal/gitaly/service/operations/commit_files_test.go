package operations

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var commitFilesMessage = []byte("Change files")

func TestUserCommitFiles(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserCommitFiles)
}

func testUserCommitFiles(t *testing.T, ctx context.Context) {
	opts := []testserver.GitalyServerOpt{
		testserver.WithSigningKey(filepath.Join(testhelper.TestdataAbsolutePath(t), "signing_gpg_key")),
	}

	ctx, cfg, client := setupOperationsService(t, ctx, opts...)

	testcfg.BuildGitalyGPG(t, cfg)

	const (
		DefaultMode    = "100644"
		ExecutableMode = "100755"
	)

	startRepo, _ := gittest.CreateRepository(t, ctx, cfg)
	targetRepoSentinel := &gitalypb.Repository{}

	type step struct {
		actions         []*gitalypb.UserCommitFilesRequest
		startRepository *gitalypb.Repository
		startBranch     string
		expectedErr     error
		repoCreated     bool
		branchCreated   bool
		treeEntries     []gittest.TreeEntry
		sign            bool
	}

	for _, tc := range []struct {
		desc  string
		steps []step
	}{
		{
			desc: "create file with .git/hooks/pre-commit",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest(".git/hooks/pre-commit"),
						actionContentRequest("content-1"),
					},
					expectedErr: structerr.NewInternal("invalid path: '.git/hooks/pre-commit'"),
				},
			},
		},
		{
			desc: "create directory",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("directory-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "directory-1/.gitkeep"},
					},
				},
			},
		},
		{
			desc: "create directory ignores mode and content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						actionRequest(&gitalypb.UserCommitFilesAction{
							UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
								Header: &gitalypb.UserCommitFilesActionHeader{
									Action:          gitalypb.UserCommitFilesActionHeader_CREATE_DIR,
									FilePath:        []byte("directory-1"),
									ExecuteFilemode: true,
									Base64Content:   true,
								},
							},
						}),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "directory-1/.gitkeep"},
					},
				},
			},
		},
		{
			desc: "create directory created duplicate",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("directory-1"),
						createDirHeaderRequest("directory-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "directory-1", errorType: errDirectoryExists}),
				},
			},
		},
		{
			desc: "create directory with traversal",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("../directory-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../directory-1", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "create directory existing duplicate",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("directory-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "directory-1/.gitkeep"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("directory-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "directory-1", errorType: errDirectoryExists}),
				},
			},
		},
		{
			desc: "create directory with files name",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("file-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "file-1", errorType: errFileExists}),
				},
			},
		},
		{
			desc: "create file with directory traversal",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("../file-1"),
						actionContentRequest("content-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../file-1", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "create file with double slash",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("invalid://file/name/here"),
						actionContentRequest("content-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "invalid://file/name/here", errorType: errInvalidPath}),
				},
			},
		},
		{
			desc: "create file without content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1"},
					},
				},
			},
		},
		{
			desc: "create file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						actionContentRequest(" content-2"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1 content-2"},
					},
				},
			},
		},
		{
			desc: "create file in directory",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("folder-1/filename/folder-1/filename"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "folder-1/filename/folder-1/filename", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "create file with unclean path",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("/file-1"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "create file with base64 content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createBase64FileHeaderRequest("file-1"),
						actionContentRequest(base64.StdEncoding.EncodeToString([]byte("content-1"))),
						actionContentRequest(base64.StdEncoding.EncodeToString([]byte(" content-2"))),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1 content-2"},
					},
				},
			},
		},
		{
			desc: "create file does not normalize line endings",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1\r\n"),
						actionContentRequest(" content-2\r\n"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1\r\n content-2\r\n"},
					},
				},
			},
		},
		{
			desc: "create duplicate file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						createFileHeaderRequest("file-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "file-1", errorType: errFileExists}),
				},
			},
		},
		{
			desc: "create file overwrites directory",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("file-1"),
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "update created file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						updateFileHeaderRequest("file-1"),
						actionContentRequest("content-2"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-2"},
					},
				},
			},
		},
		{
			desc: "update file does not normalize line endings",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						updateFileHeaderRequest("file-1"),
						actionContentRequest("content-2\r\n"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-2\r\n"},
					},
				},
			},
		},
		{
			desc: "update base64 content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						updateBase64FileHeaderRequest("file-1"),
						actionContentRequest(base64.StdEncoding.EncodeToString([]byte("content-2"))),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-2"},
					},
				},
			},
		},
		{
			desc: "update ignores mode",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						actionRequest(&gitalypb.UserCommitFilesAction{
							UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
								Header: &gitalypb.UserCommitFilesActionHeader{
									Action:          gitalypb.UserCommitFilesActionHeader_UPDATE,
									FilePath:        []byte("file-1"),
									ExecuteFilemode: true,
								},
							},
						}),
						actionContentRequest("content-2"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-2"},
					},
				},
			},
		},
		{
			desc: "update existing file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						updateFileHeaderRequest("file-1"),
						actionContentRequest("content-2"),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-2"},
					},
				},
			},
		},
		{
			desc: "update non-existing file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						updateFileHeaderRequest("non-existing"),
						actionContentRequest("content"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "non-existing", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "move file with traversal in source",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("../original-file", "moved-file", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../original-file", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "move file with traversal in destination",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("original-file", "../moved-file", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../moved-file", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "move created file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("original-file"),
						actionContentRequest("content-1"),
						moveFileHeaderRequest("original-file", "moved-file", true),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "moved-file", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "move ignores mode",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("original-file"),
						actionContentRequest("content-1"),
						actionRequest(&gitalypb.UserCommitFilesAction{
							UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
								Header: &gitalypb.UserCommitFilesActionHeader{
									Action:          gitalypb.UserCommitFilesActionHeader_MOVE,
									FilePath:        []byte("moved-file"),
									PreviousPath:    []byte("original-file"),
									ExecuteFilemode: true,
									InferContent:    true,
								},
							},
						}),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "moved-file", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "moving directory fails",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createDirHeaderRequest("directory"),
						moveFileHeaderRequest("directory", "moved-directory", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "directory", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "move file inferring content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("original-file"),
						actionContentRequest("original-content"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "original-file", Content: "original-content"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("original-file", "moved-file", true),
						actionContentRequest("ignored-content"),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "moved-file", Content: "original-content"},
					},
				},
			},
		},
		{
			desc: "move file with non-existing source",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("non-existing", "destination-file", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "non-existing", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "move file with already existing destination file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("source-file"),
						createFileHeaderRequest("already-existing"),
						moveFileHeaderRequest("source-file", "already-existing", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "already-existing", errorType: errFileExists}),
				},
			},
		},
		{
			// seems like a bug in the original implementation to allow overwriting a
			// directory
			desc: "move file with already existing destination directory",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("source-file"),
						actionContentRequest("source-content"),
						createDirHeaderRequest("already-existing"),
						moveFileHeaderRequest("source-file", "already-existing", true),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "already-existing", Content: "source-content"},
					},
				},
			},
		},
		{
			desc: "move file providing content",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("original-file"),
						actionContentRequest("original-content"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "original-file", Content: "original-content"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("original-file", "moved-file", false),
						actionContentRequest("new-content"),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "moved-file", Content: "new-content"},
					},
				},
			},
		},
		{
			desc: "move file does not normalize line endings",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("original-file"),
						actionContentRequest("original-content"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "original-file", Content: "original-content"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("original-file", "moved-file", false),
						actionContentRequest("new-content\r\n"),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "moved-file", Content: "new-content\r\n"},
					},
				},
			},
		},
		{
			desc: "mark non-existing file executable",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						chmodFileHeaderRequest("file-1", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "file-1", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "mark executable file executable",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						chmodFileHeaderRequest("file-1", true),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: ExecutableMode, Path: "file-1"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						chmodFileHeaderRequest("file-1", true),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: ExecutableMode, Path: "file-1"},
					},
				},
			},
		},
		{
			desc: "mark file executable with directory traversal",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						chmodFileHeaderRequest("../file-1", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../file-1", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "mark created file executable",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						chmodFileHeaderRequest("file-1", true),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: ExecutableMode, Path: "file-1", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "mark existing file executable",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					repoCreated:   true,
					branchCreated: true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						chmodFileHeaderRequest("file-1", true),
					},
					treeEntries: []gittest.TreeEntry{
						{Mode: ExecutableMode, Path: "file-1", Content: "content-1"},
					},
				},
			},
		},
		{
			desc: "move non-existing file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						moveFileHeaderRequest("non-existing", "should-not-be-created", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "non-existing", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "move doesn't overwrite a file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						createFileHeaderRequest("file-2"),
						actionContentRequest("content-2"),
						moveFileHeaderRequest("file-1", "file-2", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "file-2", errorType: errFileExists}),
				},
			},
		},
		{
			desc: "delete non-existing file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						deleteFileHeaderRequest("non-existing"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "non-existing", errorType: errFileNotFound}),
				},
			},
		},
		{
			desc: "delete file with directory traversal",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						deleteFileHeaderRequest("../file-1"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "../file-1", errorType: errDirectoryTraversal}),
				},
			},
		},
		{
			desc: "delete created file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
						deleteFileHeaderRequest("file-1"),
					},
					branchCreated: true,
					repoCreated:   true,
					sign:          true,
				},
			},
		},
		{
			desc: "delete existing file",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					branchCreated: true,
					repoCreated:   true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
				},
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						deleteFileHeaderRequest("file-1"),
					},
				},
			},
		},
		{
			desc: "invalid action",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						actionRequest(&gitalypb.UserCommitFilesAction{
							UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
								Header: &gitalypb.UserCommitFilesActionHeader{
									Action: -1,
								},
							},
						}),
					},
					expectedErr: structerr.NewInvalidArgument("NoMethodError: undefined method `downcase' for -1:Integer"),
				},
			},
		},
		{
			desc: "start repository refers to target repository",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					startRepository: targetRepoSentinel,
					branchCreated:   true,
					repoCreated:     true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
					sign: true,
				},
			},
		},
		{
			desc: "empty target repository with start branch set",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					startBranch:   "master",
					branchCreated: true,
					repoCreated:   true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
					sign: true,
				},
			},
		},
		{
			desc: "start repository refers to an empty remote repository",
			steps: []step{
				{
					actions: []*gitalypb.UserCommitFilesRequest{
						createFileHeaderRequest("file-1"),
						actionContentRequest("content-1"),
					},
					startBranch:     "master",
					startRepository: startRepo,
					branchCreated:   true,
					repoCreated:     true,
					treeEntries: []gittest.TreeEntry{
						{Mode: DefaultMode, Path: "file-1", Content: "content-1"},
					},
					sign: true,
				},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			const branch = "main"

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

			for i, step := range tc.steps {
				if step.startRepository == targetRepoSentinel {
					step.startRepository = repoProto
				}

				headerRequest := headerRequest(
					repoProto,
					gittest.TestUser,
					branch,
					[]byte("commit message"),
					"",
					"",
				)
				setAuthorAndEmail(headerRequest, []byte("Author Name"), []byte("author.email@example.com"))

				if step.startRepository != nil {
					setStartRepository(headerRequest, step.startRepository)
				}

				if step.startBranch != "" {
					setStartBranchName(headerRequest, []byte(step.startBranch))
				}

				if step.sign {
					header := getHeader(headerRequest)
					header.Sign = true
				}

				stream, err := client.UserCommitFiles(ctx)
				require.NoError(t, err)
				require.NoError(t, stream.Send(headerRequest))

				for j, action := range step.actions {
					require.NoError(t, stream.Send(action), "step %d, action %d", i+1, j+1)
				}

				resp, err := stream.CloseAndRecv()

				if step.expectedErr != nil {
					testhelper.RequireGrpcError(t, step.expectedErr, err)
					continue
				}

				require.NoError(t, err)
				require.Equal(t, step.branchCreated, resp.BranchUpdate.BranchCreated, "step %d", i+1)
				require.Equal(t, step.repoCreated, resp.BranchUpdate.RepoCreated, "step %d", i+1)
				gittest.RequireTree(t, cfg, repoPath, branch, step.treeEntries)

				authorDate := gittest.Exec(t, cfg, "-C", repoPath, "log", "--pretty='format:%ai'", "-1")
				require.Contains(t, string(authorDate), gittest.TimezoneOffset)

				repo := localrepo.NewTestRepo(t, cfg, repoProto)
				data, err := repo.ReadObject(ctx, git.ObjectID(resp.BranchUpdate.CommitId))
				require.NoError(t, err)

				gpgsig, dataWithoutGpgSig := signature.ExtractSignature(t, ctx, data)

				if featureflag.GPGSigning.IsEnabled(ctx) && step.sign {
					pubKey := testhelper.MustReadFile(t, "testdata/signing_gpg_key.pub")
					keyring, err := openpgp.ReadKeyRing(bytes.NewReader(pubKey))
					require.NoError(t, err)

					_, err = openpgp.CheckArmoredDetachedSignature(
						keyring,
						strings.NewReader(dataWithoutGpgSig),
						strings.NewReader(gpgsig),
						&packet.Config{},
					)
					require.NoError(t, err)
				} else {
					require.Empty(t, gpgsig)
				}
			}
		})
	}
}

func TestUserCommitFilesStableCommitID(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserCommitFilesStableCommitID)
}

func testUserCommitFilesStableCommitID(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for key, values := range testcfg.GitalyServersMetadataFromCfg(t, cfg) {
		for _, value := range values {
			ctx = metadata.AppendToOutgoingContext(ctx, key, value)
		}
	}

	stream, err := client.UserCommitFiles(ctx)
	require.NoError(t, err)

	headerRequest := headerRequest(repoProto, gittest.TestUser, "master", []byte("commit message"), "", "")
	setAuthorAndEmail(headerRequest, []byte("Author Name"), []byte("author.email@example.com"))
	setTimestamp(headerRequest, time.Unix(12345, 0))
	require.NoError(t, stream.Send(headerRequest))

	require.NoError(t, stream.Send(createFileHeaderRequest("file.txt")))
	require.NoError(t, stream.Send(actionContentRequest("content")))
	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)

	require.Equal(t, resp.BranchUpdate.CommitId, gittest.ObjectHashDependent(t, map[string]string{
		"sha1":   "23ec4ccd7fcc6ecf39431805bbff1cbcb6c23b9d",
		"sha256": "0ab6f5df19cb4387f5b1bdac29ea497e12ad4ebd6c50c0a6ade01c75d2f5c5ad",
	}))
	require.True(t, resp.BranchUpdate.BranchCreated)
	require.True(t, resp.BranchUpdate.RepoCreated)
	gittest.RequireTree(t, cfg, repoPath, "refs/heads/master", []gittest.TreeEntry{
		{Mode: "100644", Path: "file.txt", Content: "content"},
	})

	commit, err := repo.ReadCommit(ctx, "refs/heads/master")
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "23ec4ccd7fcc6ecf39431805bbff1cbcb6c23b9d",
			"sha256": "0ab6f5df19cb4387f5b1bdac29ea497e12ad4ebd6c50c0a6ade01c75d2f5c5ad",
		}),
		TreeId: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "541550ddcf8a29bcd80b0800a142a7d47890cfd6",
			"sha256": "77313ea10ef747ceeb25c7177971d55b5cc67bf41cdb56e3497e905ebcad6303",
		}),
		Subject:  []byte("commit message"),
		Body:     []byte("commit message"),
		BodySize: 14,
		Author: &gitalypb.CommitAuthor{
			Name:     []byte("Author Name"),
			Email:    []byte("author.email@example.com"),
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:     gittest.TestUser.Name,
			Email:    gittest.TestUser.Email,
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
	}, commit)
}

func TestUserCommitFilesQuarantine(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserCommitFilesQuarantine)
}

func testUserCommitFilesQuarantine(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	// Set up a hook that parses the new object and then aborts the update. Like this, we can
	// assert that the object does not end up in the main repository.
	outputPath := filepath.Join(testhelper.TempDir(t), "output")
	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		read oldval newval ref &&
		git rev-parse $newval^{commit} >%s &&
		exit 1
	`, outputPath)))

	stream, err := client.UserCommitFiles(ctx)
	require.NoError(t, err)

	headerRequest := headerRequest(repoProto, gittest.TestUser, "master", []byte("commit message"), "", "")
	setAuthorAndEmail(headerRequest, []byte("Author Name"), []byte("author.email@example.com"))
	setTimestamp(headerRequest, time.Unix(12345, 0))
	require.NoError(t, stream.Send(headerRequest))

	require.NoError(t, stream.Send(createFileHeaderRequest("file.txt")))
	require.NoError(t, stream.Send(actionContentRequest("content")))
	_, err = stream.CloseAndRecv()

	testhelper.RequireGrpcErrorContains(t, structerr.NewPermissionDenied("denied by custom hooks: update reference: running pre-receive hooks: executing custom hooks: error executing").WithDetail(
		&gitalypb.UserCommitFilesError{
			Error: &gitalypb.UserCommitFilesError_CustomHook{
				CustomHook: &gitalypb.CustomHookError{
					HookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
					Stdout:   []byte(""),
				},
			},
		},
	), err)

	hookOutput := testhelper.MustReadFile(t, outputPath)
	oid, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(hookOutput))
	require.NoError(t, err)
	exists, err := repo.HasRevision(ctx, oid.Revision()+"^{commit}")
	require.NoError(t, err)
	require.False(t, exists, "quarantined commit should have been discarded")
}

func TestSuccessfulUserCommitFilesRequest(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).
		Run(t, testSuccessfulUserCommitFilesRequest)
}

func testSuccessfulUserCommitFilesRequest(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	filePath := "héllo/wörld"
	authorName := []byte("Jane Doe")
	authorEmail := []byte("janedoe@gitlab.com")

	type setupData struct {
		repo                  *gitalypb.Repository
		repoPath              string
		branchName            string
		startBranchName       string
		expectedOldOID        git.ObjectID
		expectedRepoCreated   bool
		expectedBranchCreated bool
		executeFilemode       bool
		expectedError         error
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "existing repo and branch",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("preexisting"))

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "preexisting",
					expectedRepoCreated:   false,
					expectedBranchCreated: false,
				}
			},
		},
		{
			desc: "existing repo and branch + expectedOldOID",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("preexisting"))

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "preexisting",
					expectedOldOID:        commitID,
					expectedRepoCreated:   false,
					expectedBranchCreated: false,
				}
			},
		},
		{
			desc: "existing repo and branch + invalid expectedOldOID",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("preexisting"))

				return setupData{
					repo:           repo,
					repoPath:       repoPath,
					branchName:     "preexisting",
					expectedOldOID: "foobar",
					expectedError: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("invalid expected old object ID: invalid object ID: %q, expected length %d, got 6", "foobar", gittest.DefaultObjectHash.EncodedLen()),
						"old_object_id", "foobar",
					),
				}
			},
		},
		{
			desc: "existing repo and branch + valid expectedOldOID but invalid object",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("preexisting"))

				return setupData{
					repo:           repo,
					repoPath:       repoPath,
					branchName:     "preexisting",
					expectedOldOID: gittest.DefaultObjectHash.ZeroOID,
					expectedError: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("cannot resolve expected old object ID: reference not found"),
						"old_object_id", gittest.DefaultObjectHash.ZeroOID,
					),
				}
			},
		},
		{
			desc: "existing repo and branch + incorrect expectedOldOID",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				actualCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("preexisting"))
				expectedCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unrelated commit"))

				return setupData{
					repo:           repo,
					repoPath:       repoPath,
					branchName:     "preexisting",
					expectedOldOID: expectedCommitID,
					expectedError: testhelper.WithInterceptedMetadataItems(structerr.NewFailedPrecondition("reference update: reference does not point to expected object"),
						structerr.MetadataItem{Key: "actual_object_id", Value: actualCommitID.String()},
						structerr.MetadataItem{Key: "expected_object_id", Value: expectedCommitID.String()},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/preexisting"},
					),
				}
			},
		},
		{
			desc: "existing repo, new branch",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "new-branch",
					expectedRepoCreated:   false,
					expectedBranchCreated: true,
				}
			},
		},
		{
			desc: "existing repo, new branch, with start branch",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("start-branch"))

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "new-branch",
					startBranchName:       "start-branch",
					expectedRepoCreated:   false,
					expectedBranchCreated: true,
				}
			},
		},
		{
			desc: "empty repository",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "new-branch",
					expectedRepoCreated:   true,
					expectedBranchCreated: true,
				}
			},
		},
		{
			desc: "create executable file",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					repo:                  repo,
					repoPath:              repoPath,
					branchName:            "new-branch",
					executeFilemode:       true,
					expectedRepoCreated:   false,
					expectedBranchCreated: true,
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			headerRequest := headerRequest(setup.repo, gittest.TestUser, setup.branchName, commitFilesMessage, setup.startBranchName, setup.expectedOldOID.String())
			setAuthorAndEmail(headerRequest, authorName, authorEmail)

			actionsRequest1 := createFileHeaderRequest(filePath)
			actionsRequest2 := actionContentRequest("My")
			actionsRequest3 := actionContentRequest(" content")
			actionsRequest4 := chmodFileHeaderRequest(filePath, setup.executeFilemode)

			stream, err := client.UserCommitFiles(ctx)

			require.NoError(t, err)
			require.NoError(t, stream.Send(headerRequest))
			require.NoError(t, stream.Send(actionsRequest1))
			require.NoError(t, stream.Send(actionsRequest2))
			require.NoError(t, stream.Send(actionsRequest3))
			require.NoError(t, stream.Send(actionsRequest4))

			resp, err := stream.CloseAndRecv()
			if setup.expectedError != nil {
				testhelper.RequireGrpcError(t, setup.expectedError, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, setup.expectedRepoCreated, resp.GetBranchUpdate().GetRepoCreated())
			require.Equal(t, setup.expectedBranchCreated, resp.GetBranchUpdate().GetBranchCreated())

			headCommit, err := localrepo.NewTestRepo(t, cfg, setup.repo).ReadCommit(ctx, git.Revision(setup.branchName))
			require.NoError(t, err)
			require.Equal(t, authorName, headCommit.Author.Name)
			require.Equal(t, gittest.TestUser.Name, headCommit.Committer.Name)
			require.Equal(t, authorEmail, headCommit.Author.Email)
			require.Equal(t, gittest.TestUser.Email, headCommit.Committer.Email)
			require.Equal(t, commitFilesMessage, headCommit.Subject)

			fileContent := gittest.Exec(t, cfg, "-C", setup.repoPath, "show", headCommit.GetId()+":"+filePath)
			require.Equal(t, "My content", string(fileContent))

			commitInfo := gittest.Exec(t, cfg, "-C", setup.repoPath, "show", headCommit.GetId())
			expectedFilemode := "100644"
			if setup.executeFilemode {
				expectedFilemode = "100755"
			}
			require.Contains(t, string(commitInfo), fmt.Sprint("new file mode ", expectedFilemode))
		})
	}
}

func TestUserCommitFiles_move(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testUserCommitFilesMove)
}

func testUserCommitFilesMove(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	branchName := "master"
	previousFilePath := "README"
	filePath := "NEWREADME"
	authorName := []byte("Jane Doe")
	authorEmail := []byte("janedoe@gitlab.com")
	fileContent := "file content"

	for _, tc := range []struct {
		desc            string
		content         string
		infer           bool
		expectedContent string
	}{
		{
			desc:            "empty content without inferred content creates empty file",
			content:         "",
			infer:           false,
			expectedContent: "",
		},
		{
			desc:            "replace content",
			content:         "foo",
			infer:           false,
			expectedContent: "foo",
		},
		{
			desc:            "inferred content retains preexisting content",
			content:         "",
			infer:           true,
			expectedContent: fileContent,
		},
		{
			desc:            "inferred content overrides provided content",
			content:         "foo",
			infer:           true,
			expectedContent: fileContent,
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			testRepo, testRepoPath := gittest.CreateRepository(t, ctx, cfg)
			gittest.WriteCommit(t, cfg, testRepoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
				gittest.TreeEntry{Path: "README", Mode: "100644", Content: fileContent},
			))

			headerRequest := headerRequest(testRepo, gittest.TestUser, branchName, commitFilesMessage, "", "")
			setAuthorAndEmail(headerRequest, authorName, authorEmail)
			moveRequest := moveFileHeaderRequest(previousFilePath, filePath, tc.infer)

			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(headerRequest))
			require.NoError(t, stream.Send(moveRequest))

			if len(tc.content) > 0 {
				contentRequest := actionContentRequest(tc.content)
				require.NoError(t, stream.Send(contentRequest))
			}

			resp, err := stream.CloseAndRecv()
			require.NoError(t, err)

			update := resp.GetBranchUpdate()
			require.NotNil(t, update)

			fileContent := gittest.Exec(t, cfg, "-C", testRepoPath, "show", update.CommitId+":"+filePath)
			require.Equal(t, tc.expectedContent, string(fileContent))
		})
	}
}

func TestSuccessUserCommitFilesRequestForceCommit(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).
		Run(t, testSuccessUserCommitFilesRequestForceCommit)
}

func testSuccessUserCommitFilesRequestForceCommit(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	authorName := []byte("Jane Doe")
	authorEmail := []byte("janedoe@gitlab.com")
	targetBranchName := "feature"
	startBranchName := []byte("master")

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	baseCommitID := gittest.WriteCommit(t, cfg, repoPath)
	startBranchCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommitID), gittest.WithMessage("master"), gittest.WithBranch("master"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommitID), gittest.WithMessage("target"), gittest.WithBranch("target"))

	headerRequest := headerRequest(repoProto, gittest.TestUser, targetBranchName, commitFilesMessage, "", "")
	setAuthorAndEmail(headerRequest, authorName, authorEmail)
	setStartBranchName(headerRequest, startBranchName)
	setForce(headerRequest, true)

	stream, err := client.UserCommitFiles(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(headerRequest))
	require.NoError(t, stream.Send(createFileHeaderRequest("TEST.md")))
	require.NoError(t, stream.Send(actionContentRequest("Test")))

	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)

	update := resp.GetBranchUpdate()
	newTargetBranchCommit, err := repo.ReadCommit(ctx, git.Revision(targetBranchName))
	require.NoError(t, err)

	require.Equal(t, update.CommitId, newTargetBranchCommit.Id)
	require.Equal(t, []string{startBranchCommitID.String()}, newTargetBranchCommit.ParentIds)
}

func TestSuccessUserCommitFilesRequestStartSha(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).
		Run(t, testSuccessUserCommitFilesRequestStartSha)
}

func testSuccessUserCommitFilesRequestStartSha(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	startCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

	targetBranchName := "new"

	headerRequest := headerRequest(repoProto, gittest.TestUser, targetBranchName, commitFilesMessage, "", "")
	setStartSha(headerRequest, startCommitID.String())

	stream, err := client.UserCommitFiles(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(headerRequest))
	require.NoError(t, stream.Send(createFileHeaderRequest("TEST.md")))
	require.NoError(t, stream.Send(actionContentRequest("Test")))

	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)

	update := resp.GetBranchUpdate()
	newTargetBranchCommit, err := repo.ReadCommit(ctx, git.Revision(targetBranchName))
	require.NoError(t, err)

	require.Equal(t, update.CommitId, newTargetBranchCommit.Id)
	require.Equal(t, []string{startCommitID.String()}, newTargetBranchCommit.ParentIds)
}

func TestUserCommitFiles_remoteRepository(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testUserCommitFilesRemoteRepository)
}

func testUserCommitFilesRemoteRepository(t *testing.T, ctx context.Context) {
	t.Parallel()

	for _, tc := range []struct {
		desc         string
		setupRequest func(*testing.T, context.Context, config.Cfg, *gitalypb.UserCommitFilesRequest) git.ObjectID
	}{
		{
			desc: "object ID",
			setupRequest: func(t *testing.T, ctx context.Context, cfg config.Cfg, request *gitalypb.UserCommitFilesRequest) git.ObjectID {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, repoPath)

				setStartSha(request, commitID.String())
				setStartRepository(request, repoProto)

				return commitID
			},
		},
		{
			desc: "branch name",
			setupRequest: func(t *testing.T, ctx context.Context, cfg config.Cfg, request *gitalypb.UserCommitFilesRequest) git.ObjectID {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

				setStartBranchName(request, []byte("master"))
				setStartRepository(request, repoProto)

				return commitID
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx, cfg, client := setupOperationsService(t, ctx)

			newRepoProto, _ := gittest.CreateRepository(t, ctx, cfg)
			newRepo := localrepo.NewTestRepo(t, cfg, newRepoProto)

			targetBranchName := "new"

			headerRequest := headerRequest(newRepoProto, gittest.TestUser, targetBranchName, commitFilesMessage, "", "")
			startCommitID := tc.setupRequest(t, ctx, cfg, headerRequest)

			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(headerRequest))
			require.NoError(t, stream.Send(createFileHeaderRequest("TEST.md")))
			require.NoError(t, stream.Send(actionContentRequest("Test")))

			resp, err := stream.CloseAndRecv()
			require.NoError(t, err)

			update := resp.GetBranchUpdate()
			newTargetBranchCommit, err := newRepo.ReadCommit(ctx, git.Revision(targetBranchName))
			require.NoError(t, err)

			require.Equal(t, update.CommitId, newTargetBranchCommit.Id)
			require.Equal(t, []string{startCommitID.String()}, newTargetBranchCommit.ParentIds)
		})
	}
}

func TestSuccessfulUserCommitFilesRequestWithSpecialCharactersInSignature(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).
		Run(t, testSuccessfulUserCommitFilesRequestWithSpecialCharactersInSignature)
}

func testSuccessfulUserCommitFilesRequestWithSpecialCharactersInSignature(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, _ := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	targetBranchName := "master"

	testCases := []struct {
		desc   string
		user   *gitalypb.User
		author *gitalypb.CommitAuthor // expected value
	}{
		{
			desc:   "special characters at start and end",
			user:   &gitalypb.User{Name: []byte(",:;<>\"'\nJane Doe,:;<>'\"\n"), Email: []byte(",:;<>'\"\njanedoe@gitlab.com,:;<>'\"\n"), GlId: gittest.GlID},
			author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe"), Email: []byte("janedoe@gitlab.com")},
		},
		{
			desc:   "special characters in the middle",
			user:   &gitalypb.User{Name: []byte("Ja<ne\n D>oe"), Email: []byte("ja<ne\ndoe>@gitlab.com"), GlId: gittest.GlID},
			author: &gitalypb.CommitAuthor{Name: []byte("Jane Doe"), Email: []byte("janedoe@gitlab.com")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			headerRequest := headerRequest(repoProto, tc.user, targetBranchName, commitFilesMessage, "", "")
			setAuthorAndEmail(headerRequest, tc.user.Name, tc.user.Email)

			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(headerRequest))

			_, err = stream.CloseAndRecv()
			require.NoError(t, err)

			newCommit, err := repo.ReadCommit(ctx, git.Revision(targetBranchName))
			require.NoError(t, err)

			require.Equal(t, tc.author.Name, newCommit.Author.Name, "author name")
			require.Equal(t, tc.author.Email, newCommit.Author.Email, "author email")
			require.Equal(t, tc.author.Name, newCommit.Committer.Name, "committer name")
			require.Equal(t, tc.author.Email, newCommit.Committer.Email, "committer email")
		})
	}
}

func TestFailedUserCommitFilesRequestDueToHooks(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testFailedUserCommitFilesRequestDueToHooks)
}

func testFailedUserCommitFilesRequestDueToHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"))

	branchName := "feature"
	filePath := "my/file.txt"
	headerRequest := headerRequest(repoProto, gittest.TestUser, branchName, commitFilesMessage, "", "")
	actionsRequest1 := createFileHeaderRequest(filePath)
	actionsRequest2 := actionContentRequest("My content")
	hookContent := []byte("#!/bin/sh\nprintenv | grep -e GL_ID -e GL_USERNAME | sort | paste -sd ' ' -\nexit 1")

	for _, hookName := range GitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(headerRequest))
			require.NoError(t, stream.Send(actionsRequest1))
			require.NoError(t, stream.Send(actionsRequest2))

			_, err = stream.CloseAndRecv()

			var hookT gitalypb.CustomHookError_HookType
			if hookName == "pre-receive" {
				hookT = gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE
			} else {
				hookT = gitalypb.CustomHookError_HOOK_TYPE_UPDATE
			}

			expectedOut := fmt.Sprintf("GL_ID=%s GL_USERNAME=%s\n",
				gittest.TestUser.GlId, gittest.TestUser.GlUsername)

			testhelper.RequireGrpcError(t, structerr.NewPermissionDenied("denied by custom hooks: update reference: running %s hooks: %s", hookName, expectedOut).WithDetail(
				&gitalypb.UserCommitFilesError{
					Error: &gitalypb.UserCommitFilesError_CustomHook{
						CustomHook: &gitalypb.CustomHookError{
							HookType: hookT,
							Stdout:   []byte(expectedOut),
						},
					},
				},
			), err)
		})
	}
}

func TestFailedUserCommitFilesRequestDueToIndexError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testFailedUserCommitFilesRequestDueToIndexError)
}

func testFailedUserCommitFilesRequestDueToIndexError(t *testing.T, ctx context.Context) {
	ctx, cfg, client := setupOperationsService(t, ctx)

	type setupData struct {
		requests    []*gitalypb.UserCommitFilesRequest
		expectedErr error
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "file already exists",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "preexisting", Mode: "100644", Content: "something"},
				))

				return setupData{
					requests: []*gitalypb.UserCommitFilesRequest{
						headerRequest(repo, gittest.TestUser, "feature", commitFilesMessage, "", ""),
						createFileHeaderRequest("preexisting"),
						actionContentRequest("This file already exists"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "preexisting", errorType: errFileExists}),
				}
			},
		},
		{
			desc: "file doesn't exists",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"))

				return setupData{
					requests: []*gitalypb.UserCommitFilesRequest{
						headerRequest(repo, gittest.TestUser, "feature", commitFilesMessage, "", ""),
						chmodFileHeaderRequest("nonexistent", true),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "nonexistent", errorType: errFileNotFound}),
				}
			},
		},
		{
			desc: "dir already exists",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "dir", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{})},
				))

				return setupData{
					requests: []*gitalypb.UserCommitFilesRequest{
						headerRequest(repo, gittest.TestUser, "feature", commitFilesMessage, "", ""),
						actionRequest(&gitalypb.UserCommitFilesAction{
							UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
								Header: &gitalypb.UserCommitFilesActionHeader{
									Action:        gitalypb.UserCommitFilesActionHeader_CREATE_DIR,
									Base64Content: false,
									FilePath:      []byte("dir"),
								},
							},
						}),
						actionContentRequest("This file already exists, as a directory"),
					},
					expectedErr: structuredIndexError(t, &indexError{path: "dir", errorType: errDirectoryExists}),
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)

			for _, req := range setup.requests {
				require.NoError(t, stream.Send(req))
			}

			_, err = stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
		})
	}
}

func TestFailedUserCommitFilesRequest(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testFailedUserCommitFilesRequest)
}

func testFailedUserCommitFilesRequest(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	branchName := "feature"

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName))

	testCases := []struct {
		desc        string
		req         *gitalypb.UserCommitFilesRequest
		expectedErr error
	}{
		{
			desc:        "unset repository",
			req:         headerRequest(nil, gittest.TestUser, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:        "empty User",
			req:         headerRequest(repo, nil, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("empty User"),
		},
		{
			desc:        "empty BranchName",
			req:         headerRequest(repo, gittest.TestUser, "", commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("empty BranchName"),
		},
		{
			desc:        "empty CommitMessage",
			req:         headerRequest(repo, gittest.TestUser, branchName, nil, "", ""),
			expectedErr: structerr.NewInvalidArgument("empty CommitMessage"),
		},
		{
			desc:        "invalid object ID: \"foobar\"",
			req:         setStartSha(headerRequest(repo, gittest.TestUser, branchName, commitFilesMessage, "", ""), "foobar"),
			expectedErr: structerr.NewInvalidArgument(`invalid object ID: "foobar", expected length %v, got 6`, gittest.DefaultObjectHash.EncodedLen()),
		},
		{
			desc:        "failed to parse signature - Signature cannot have an empty name or email 1",
			req:         headerRequest(repo, &gitalypb.User{}, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("commit: failed to parse signature - Signature cannot have an empty name or email"),
		},
		{
			desc:        "failed to parse signature - Signature cannot have an empty name or email 2",
			req:         headerRequest(repo, &gitalypb.User{Name: []byte(""), Email: []byte("")}, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("commit: failed to parse signature - Signature cannot have an empty name or email"),
		},
		{
			desc:        "failed to parse signature - Signature cannot have an empty name or email 3",
			req:         headerRequest(repo, &gitalypb.User{Name: []byte(" "), Email: []byte(" ")}, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("commit: failed to parse signature - Signature cannot have an empty name or email"),
		},
		{
			desc:        "failed to parse signature - Signature cannot have an empty name or email 4",
			req:         headerRequest(repo, &gitalypb.User{Name: []byte("Jane Doe"), Email: []byte("")}, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("commit: failed to parse signature - Signature cannot have an empty name or email"),
		},
		{
			desc:        "failed to parse signature - Signature cannot have an empty name or email 5",
			req:         headerRequest(repo, &gitalypb.User{Name: []byte(""), Email: []byte("janedoe@gitlab.com")}, branchName, commitFilesMessage, "", ""),
			expectedErr: structerr.NewInvalidArgument("commit: failed to parse signature - Signature cannot have an empty name or email"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.UserCommitFiles(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(tc.req))

			_, err = stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestUserCommitFilesFailsIfRepositoryMissing(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(featureflag.GPGSigning).Run(t, testUserCommitFilesFailsIfRepositoryMissing)
}

func testUserCommitFilesFailsIfRepositoryMissing(t *testing.T, ctx context.Context) {
	ctx, cfg, client := setupOperationsService(t, ctx)
	repo := &gitalypb.Repository{
		StorageName:   cfg.Storages[0].Name,
		RelativePath:  t.Name(),
		GlRepository:  gittest.GlRepository,
		GlProjectPath: gittest.GlProjectPath,
	}

	branchName := "feature"
	req := headerRequest(repo, gittest.TestUser, branchName, commitFilesMessage, "", "")

	stream, err := client.UserCommitFiles(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(req))

	_, err = stream.CloseAndRecv()
	testhelper.RequireGrpcError(t, testhelper.ToInterceptedMetadata(
		structerr.New("%w", storage.NewRepositoryNotFoundError(repo.StorageName, repo.RelativePath)),
	), err)
}

func headerRequest(repo *gitalypb.Repository, user *gitalypb.User, branchName string, commitMessage []byte, startBranchName string, expectedOldOID string) *gitalypb.UserCommitFilesRequest {
	return &gitalypb.UserCommitFilesRequest{
		UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Header{
			Header: &gitalypb.UserCommitFilesRequestHeader{
				Repository:      repo,
				User:            user,
				BranchName:      []byte(branchName),
				CommitMessage:   commitMessage,
				StartBranchName: []byte(startBranchName),
				StartRepository: nil,
				ExpectedOldOid:  expectedOldOID,
			},
		},
	}
}

func setAuthorAndEmail(headerRequest *gitalypb.UserCommitFilesRequest, authorName, authorEmail []byte) {
	header := getHeader(headerRequest)
	header.CommitAuthorName = authorName
	header.CommitAuthorEmail = authorEmail
}

func setTimestamp(headerRequest *gitalypb.UserCommitFilesRequest, time time.Time) {
	getHeader(headerRequest).Timestamp = timestamppb.New(time)
}

func setStartBranchName(headerRequest *gitalypb.UserCommitFilesRequest, startBranchName []byte) {
	header := getHeader(headerRequest)
	header.StartBranchName = startBranchName
}

func setStartRepository(headerRequest *gitalypb.UserCommitFilesRequest, startRepository *gitalypb.Repository) {
	header := getHeader(headerRequest)
	header.StartRepository = startRepository
}

func setStartSha(headerRequest *gitalypb.UserCommitFilesRequest, startSha string) *gitalypb.UserCommitFilesRequest {
	header := getHeader(headerRequest)
	header.StartSha = startSha

	return headerRequest
}

func setForce(headerRequest *gitalypb.UserCommitFilesRequest, force bool) {
	header := getHeader(headerRequest)
	header.Force = force
}

func getHeader(headerRequest *gitalypb.UserCommitFilesRequest) *gitalypb.UserCommitFilesRequestHeader {
	return headerRequest.UserCommitFilesRequestPayload.(*gitalypb.UserCommitFilesRequest_Header).Header
}

func createDirHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:   gitalypb.UserCommitFilesActionHeader_CREATE_DIR,
				FilePath: []byte(filePath),
			},
		},
	})
}

func createFileHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:        gitalypb.UserCommitFilesActionHeader_CREATE,
				Base64Content: false,
				FilePath:      []byte(filePath),
			},
		},
	})
}

func createBase64FileHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:        gitalypb.UserCommitFilesActionHeader_CREATE,
				Base64Content: true,
				FilePath:      []byte(filePath),
			},
		},
	})
}

func updateFileHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:   gitalypb.UserCommitFilesActionHeader_UPDATE,
				FilePath: []byte(filePath),
			},
		},
	})
}

func updateBase64FileHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:        gitalypb.UserCommitFilesActionHeader_UPDATE,
				FilePath:      []byte(filePath),
				Base64Content: true,
			},
		},
	})
}

func chmodFileHeaderRequest(filePath string, executeFilemode bool) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:          gitalypb.UserCommitFilesActionHeader_CHMOD,
				FilePath:        []byte(filePath),
				ExecuteFilemode: executeFilemode,
			},
		},
	})
}

func moveFileHeaderRequest(previousPath, filePath string, infer bool) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:       gitalypb.UserCommitFilesActionHeader_MOVE,
				FilePath:     []byte(filePath),
				PreviousPath: []byte(previousPath),
				InferContent: infer,
			},
		},
	})
}

func deleteFileHeaderRequest(filePath string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Header{
			Header: &gitalypb.UserCommitFilesActionHeader{
				Action:   gitalypb.UserCommitFilesActionHeader_DELETE,
				FilePath: []byte(filePath),
			},
		},
	})
}

func actionContentRequest(content string) *gitalypb.UserCommitFilesRequest {
	return actionRequest(&gitalypb.UserCommitFilesAction{
		UserCommitFilesActionPayload: &gitalypb.UserCommitFilesAction_Content{
			Content: []byte(content),
		},
	})
}

func actionRequest(action *gitalypb.UserCommitFilesAction) *gitalypb.UserCommitFilesRequest {
	return &gitalypb.UserCommitFilesRequest{
		UserCommitFilesRequestPayload: &gitalypb.UserCommitFilesRequest_Action{
			Action: action,
		},
	}
}

func structuredIndexError(tb testing.TB, indexErr *indexError) error {
	return errWithDetails(tb,
		indexErr.StructuredError(),
		&gitalypb.UserCommitFilesError{
			Error: &gitalypb.UserCommitFilesError_IndexUpdate{
				IndexUpdate: indexErr.Proto(),
			},
		},
	)
}
