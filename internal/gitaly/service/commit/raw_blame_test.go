package commit

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestRawBlame(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(t, ctx)

	type setupData struct {
		request      *gitalypb.RawBlameRequest
		expectedErr  error
		expectedData string
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request:     &gitalypb.RawBlameRequest{},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "unknown storage",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: &gitalypb.Repository{
							StorageName:  "fake",
							RelativePath: "path",
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"%w", storage.NewStorageNotFoundError("fake"),
					)),
				}
			},
		},
		{
			desc: "unset revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "unset path",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte("abcdef"),
					},
					expectedErr: structerr.NewInvalidArgument("empty Path"),
				}
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte("--output=/meow"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't start with '-'"),
				}
			},
		},
		{
			desc: "invalid range",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte("abcdef"),
						Path:       []byte("a/b/c"),
						Range:      []byte("foo"),
					},
					expectedErr: structerr.NewInvalidArgument("invalid Range"),
				}
			},
		},
		{
			desc: "out-of-range",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// We write a file with three lines, only, but the request asks us to blame line 10.
				commit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\nb\nc\n"},
				))

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commit),
						Path:       []byte("path"),
						Range:      []byte("10,10"),
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.NewInvalidArgument("range is outside of the file length").
							WithMetadata("revision", commit.String()).
							WithMetadata("path", "path").
							WithMetadata("lines", 3).
							WithDetail(&gitalypb.RawBlameError{
								Error: &gitalypb.RawBlameError_OutOfRange{
									OutOfRange: &gitalypb.RawBlameError_OutOfRangeError{
										ActualLines: 3,
									},
								},
							}),
					),
				}
			},
		},
		{
			desc: "missing path",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commit := gittest.WriteCommit(t, cfg, repoPath)

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commit),
						Path:       []byte("does-not-exist"),
						Range:      []byte("1,1"),
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.NewNotFound("path not found in revision").
							WithMetadata("revision", commit.String()).
							WithMetadata("path", "does-not-exist").
							WithDetail(&gitalypb.RawBlameError{
								Error: &gitalypb.RawBlameError_PathNotFound{
									PathNotFound: &gitalypb.PathNotFoundError{
										Path: []byte("does-not-exist"),
									},
								},
							}),
					),
				}
			},
		},
		{
			desc: "path escapes repository root",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commit := gittest.WriteCommit(t, cfg, repoPath)
				escapingPath := "im/gonna/../../../escape"

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commit),
						Path:       []byte(escapingPath),
						Range:      []byte("1,1"),
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.NewInvalidArgument("path escapes repository root").
							WithMetadata("path", escapingPath),
					),
				}
			},
		},
		{
			desc: "simple blame",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitA := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\n"},
				))
				commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "b\n"},
				), gittest.WithParents(commitA))

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commitB),
						Path:       []byte("path"),
					},
					expectedData: fmt.Sprintf(`%s 1 1 1
author Scrooge McDuck
author-mail <scrooge@mcduck.com>
author-time 1572776879
author-tz +0100
committer Scrooge McDuck
committer-mail <scrooge@mcduck.com>
committer-time 1572776879
committer-tz +0100
summary message
previous %s path
filename path
	b
`, commitB, commitA),
				}
			},
		},
		{
			desc: "blame with relative path",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitA := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\n"},
				))
				commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "b\n"},
				), gittest.WithParents(commitA))

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commitB),
						Path:       []byte("foo/../path"),
					},
					expectedData: fmt.Sprintf(`%s 1 1 1
author Scrooge McDuck
author-mail <scrooge@mcduck.com>
author-time 1572776879
author-tz +0100
committer Scrooge McDuck
committer-mail <scrooge@mcduck.com>
committer-time 1572776879
committer-tz +0100
summary message
previous %s path
filename path
	b
`, commitB, commitA),
				}
			},
		},
		{
			desc: "blame with empty file",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				emptyBlob := gittest.WriteBlob(t, cfg, repoPath, []byte{})
				commitA := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", OID: emptyBlob},
				))
				commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", OID: emptyBlob},
				), gittest.WithParents(commitA))

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commitB),
						Path:       []byte("path"),
					},
					expectedData: "",
				}
			},
		},
		{
			desc: "blame with range",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitA := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\n1\n1\n1\n1\n1\na\n"},
				))
				commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "path", Mode: "100644", Content: "b\n1\n1\n1\n1\n1\nb\n"},
				), gittest.WithParents(commitA))

				return setupData{
					request: &gitalypb.RawBlameRequest{
						Repository: repo,
						Revision:   []byte(commitB),
						Path:       []byte("path"),
						Range:      []byte("6,8"),
					},
					expectedData: fmt.Sprintf(`%s 6 6 1
author Scrooge McDuck
author-mail <scrooge@mcduck.com>
author-time 1572776879
author-tz +0100
committer Scrooge McDuck
committer-mail <scrooge@mcduck.com>
committer-time 1572776879
committer-tz +0100
summary message
boundary
filename path
	1
%s 7 7 1
author Scrooge McDuck
author-mail <scrooge@mcduck.com>
author-time 1572776879
author-tz +0100
committer Scrooge McDuck
committer-mail <scrooge@mcduck.com>
committer-time 1572776879
committer-tz +0100
summary message
previous %s path
filename path
	b
`, commitA, commitB, commitA),
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			stream, err := client.RawBlame(ctx, setup.request)
			require.NoError(t, err)

			reader := streamio.NewReader(func() ([]byte, error) {
				response, err := stream.Recv()
				return response.GetData(), err
			})

			data, err := io.ReadAll(reader)
			testhelper.RequireGrpcError(t, setup.expectedErr, err, protocmp.SortRepeatedFields(&spb.Status{}, "details"))
			require.Equal(t, setup.expectedData, string(data))
		})
	}
}
