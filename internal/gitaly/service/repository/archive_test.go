//go:build !gitaly_test_sha256

package repository

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/smudge"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc/codes"
)

const (
	secretToken = "topsecret"
	lfsBody     = "hello world\n"
)

func TestGetArchive_success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(ctx, t)

	formats := []gitalypb.GetArchiveRequest_Format{
		gitalypb.GetArchiveRequest_ZIP,
		gitalypb.GetArchiveRequest_TAR,
		gitalypb.GetArchiveRequest_TAR_GZ,
		gitalypb.GetArchiveRequest_TAR_BZ2,
	}

	testCases := []struct {
		desc      string
		prefix    string
		commitID  string
		path      []byte
		exclude   [][]byte
		elidePath bool
		contents  []string
		excluded  []string
	}{
		{
			desc:     "without-prefix",
			commitID: "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			prefix:   "",
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with-prefix",
			commitID: "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			prefix:   "my-prefix",
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path as blank string",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte(""),
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path as nil",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     nil,
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
		},
		{
			desc:     "with path",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte("files"),
			contents: []string{"/whitespace", "/html/500.html"},
		},
		{
			desc:     "with path and trailing slash",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			path:     []byte("files/"),
			contents: []string{"/whitespace", "/html/500.html"},
		},
		{
			desc:     "with exclusion",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:   "",
			exclude:  [][]byte{[]byte("files")},
			contents: []string{"/.gitignore", "/LICENSE", "/README.md"},
			excluded: []string{"/files/whitespace", "/files/html/500.html"},
		},
		{
			desc:      "with path elision",
			commitID:  "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:    "my-prefix",
			elidePath: true,
			path:      []byte("files/"),
			contents:  []string{"/whitespace", "/html/500.html"},
		},
		{
			desc:      "with path elision and exclusion",
			commitID:  "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:    "my-prefix",
			elidePath: true,
			path:      []byte("files/"),
			exclude:   [][]byte{[]byte("files/images")},
			contents:  []string{"/whitespace", "/html/500.html"},
			excluded:  []string{"/images/emoji.png"},
		},
		{
			desc:      "with path elision at root",
			commitID:  "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:    "my-prefix",
			elidePath: true,
			contents:  []string{"/files/whitespace", "/files/html/500.html"},
		},
	}

	for _, tc := range testCases {
		// Run test case with each format
		for _, format := range formats {
			testCaseName := fmt.Sprintf("%s-%s", tc.desc, format.String())
			t.Run(testCaseName, func(t *testing.T) {
				req := &gitalypb.GetArchiveRequest{
					Repository: repo,
					CommitId:   tc.commitID,
					Prefix:     tc.prefix,
					Format:     format,
					Path:       tc.path,
					Exclude:    tc.exclude,
					ElidePath:  tc.elidePath,
				}
				stream, err := client.GetArchive(ctx, req)
				require.NoError(t, err)

				data, err := consumeArchive(stream)
				require.NoError(t, err)

				contents := compressedFileContents(t, format, data)

				for _, content := range tc.contents {
					require.Contains(t, contents, tc.prefix+content)
				}

				for _, excluded := range tc.excluded {
					require.NotContains(t, contents, tc.prefix+excluded)
				}
			})
		}
	}
}

func TestGetArchiveIncludeLfsBlobs(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UploadPackHideRefs).Run(t, testGetArchiveIncludeLfsBlobs)
}

func testGetArchiveIncludeLfsBlobs(t *testing.T, ctx context.Context) {
	t.Parallel()

	defaultOptions := gitlab.TestServerOptions{
		SecretToken: secretToken,
		LfsBody:     lfsBody,
	}

	url, cleanup := gitlab.NewTestServer(t, defaultOptions)
	t.Cleanup(cleanup)

	shellDir := testhelper.TempDir(t)

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
		GitlabShell: config.GitlabShell{Dir: shellDir},
		Gitlab: config.Gitlab{
			URL:        url,
			SecretFile: gitlab.WriteShellSecretFile(t, shellDir, defaultOptions.SecretToken),
		},
	}))

	client, serverSocketPath := runRepositoryService(t, cfg, nil)
	cfg.SocketPath = serverSocketPath

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	testcfg.BuildGitalyLFSSmudge(t, cfg)

	// lfs-moar branch SHA
	sha := "46abbb087fcc0fd02c340f0f2f052bd2c7708da3"

	testCases := []struct {
		desc            string
		prefix          string
		path            []byte
		includeLfsBlobs bool
	}{
		{
			desc:            "without prefix and with LFS blobs",
			prefix:          "",
			includeLfsBlobs: true,
		},
		{
			desc:            "without prefix and without LFS blobs",
			prefix:          "",
			includeLfsBlobs: false,
		},
		{
			desc:            "with prefix and with LFS blobs",
			prefix:          "my-prefix",
			includeLfsBlobs: true,
		},
		{
			desc:            "with prefix and without LFS blobs",
			prefix:          "my-prefix",
			includeLfsBlobs: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &gitalypb.GetArchiveRequest{
				Repository:      repo,
				CommitId:        sha,
				Prefix:          tc.prefix,
				Format:          gitalypb.GetArchiveRequest_ZIP,
				Path:            tc.path,
				IncludeLfsBlobs: tc.includeLfsBlobs,
			}
			stream, err := client.GetArchive(ctx, req)
			require.NoError(t, err)

			data, err := consumeArchive(stream)
			require.NoError(t, err)
			reader := bytes.NewReader(data)

			zipReader, err := zip.NewReader(reader, int64(reader.Len()))
			require.NoError(t, err)

			lfsFiles := []string{"/30170.lfs", "/another.lfs"}
			for _, lfsFile := range lfsFiles {
				found := false
				for _, f := range zipReader.File {
					if f.Name != tc.prefix+lfsFile {
						continue
					}

					found = true

					fc, err := f.Open()
					require.NoError(t, err)
					defer fc.Close()

					data, err := io.ReadAll(fc)
					require.NoError(t, err)

					if tc.includeLfsBlobs {
						require.Equal(t, lfsBody, string(data))
					} else {
						require.Contains(t, string(data), "oid sha256:")
					}
				}

				require.True(t, found, "expected to find LFS file")
			}
		})
	}
}

func TestGetArchive_inputValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(ctx, t)

	commitID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	testCases := []struct {
		desc      string
		repo      *gitalypb.Repository
		prefix    string
		commitID  string
		format    gitalypb.GetArchiveRequest_Format
		path      []byte
		exclude   [][]byte
		elidePath bool
		code      codes.Code
	}{
		{
			desc:     "Repository doesn't exist",
			repo:     &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Repository is nil",
			repo:     nil,
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "CommitId is empty",
			repo:     repo,
			prefix:   "",
			commitID: "",
			format:   gitalypb.GetArchiveRequest_ZIP,
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Format is invalid",
			repo:     repo,
			prefix:   "",
			commitID: "",
			format:   gitalypb.GetArchiveRequest_Format(-1),
			code:     codes.InvalidArgument,
		},
		{
			desc:     "Non-existing path in repository",
			repo:     repo,
			prefix:   "",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("unknown-path"),
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "Non-existing path in repository on commit ID",
			repo:     repo,
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("files/"),
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "Non-existing exclude path in repository on commit ID",
			repo:     repo,
			prefix:   "",
			commitID: commitID,
			format:   gitalypb.GetArchiveRequest_ZIP,
			exclude:  [][]byte{[]byte("files/")},
			code:     codes.FailedPrecondition,
		},
		{
			desc:     "path contains directory traversal outside repository root",
			repo:     repo,
			prefix:   "",
			commitID: "1e292f8fedd741b75372e19097c76d327140c312",
			format:   gitalypb.GetArchiveRequest_ZIP,
			path:     []byte("../../foo"),
			code:     codes.InvalidArgument,
		},
		{
			desc:     "repo missing fields",
			repo:     &gitalypb.Repository{StorageName: repo.GetStorageName()},
			prefix:   "qwert",
			commitID: "sadf",
			format:   gitalypb.GetArchiveRequest_TAR,
			path:     []byte("Here is a string...."),
			code:     codes.InvalidArgument,
		},
		{
			desc:      "with path is file and path elision",
			repo:      repo,
			commitID:  "1e292f8fedd741b75372e19097c76d327140c312",
			prefix:    "my-prefix",
			elidePath: true,
			path:      []byte("files/html/500.html"),
			code:      codes.Unknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			req := &gitalypb.GetArchiveRequest{
				Repository: tc.repo,
				CommitId:   tc.commitID,
				Prefix:     tc.prefix,
				Format:     tc.format,
				Path:       tc.path,
				Exclude:    tc.exclude,
				ElidePath:  tc.elidePath,
			}
			stream, err := client.GetArchive(ctx, req)
			require.NoError(t, err)

			_, err = consumeArchive(stream)
			testhelper.RequireGrpcCode(t, err, tc.code)
		})
	}
}

func TestGetArchive_pathInjection(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupRepositoryService(ctx, t)

	// It used to be possible to inject options into `git-archive(1)`, with the worst outcome
	// being that an adversary may create or overwrite arbitrary files in the filesystem in case
	// they passed `--output`.
	//
	// We pretend that the adversary wants to override a fixed file `/non/existent`. In
	// practice, thus would be something more interesting like `/etc/shadow` or `allowed_keys`.
	// We then create a file inside the repository itself that has `--output=/non/existent` as
	// relative path. This is done to verify that git-archive(1) indeed treats the parameter as
	// a path and does not interpret it as an option.
	outputPath := "/non/existent"

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "--output=", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
			{Path: "non", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
				{Path: "existent", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "injected file", Mode: "100644", Content: "injected content"},
				})},
			})},
		})},
	})))

	// And now we fire the request path our bogus path to try and overwrite the output path.
	stream, err := client.GetArchive(ctx, &gitalypb.GetArchiveRequest{
		Repository: repo,
		CommitId:   commitID.String(),
		Prefix:     "",
		Format:     gitalypb.GetArchiveRequest_TAR,
		Path:       []byte("--output=" + outputPath),
	})
	require.NoError(t, err)

	content, err := consumeArchive(stream)
	require.NoError(t, err)

	require.NoFileExists(t, outputPath)
	require.Equal(t,
		strings.Join([]string{
			"/",
			"/--output=/",
			"/--output=/non/",
			"/--output=/non/existent/",
			"/--output=/non/existent/injected file",
		}, "\n"),
		compressedFileContents(t, gitalypb.GetArchiveRequest_TAR, content),
	)
}

func TestGetArchive_environment(t *testing.T) {
	t.Parallel()

	testhelper.SkipWithPraefect(t, "It's not possible to create repositories through the API with the git command overwritten by the script.")

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	gitCmdFactory := gittest.NewInterceptingCommandFactory(ctx, t, cfg, func(git.ExecutionEnvironment) string {
		return `#!/bin/sh
		env | grep -E '^GL_|CORRELATION|GITALY_'
		`
	})

	testcfg.BuildGitalyHooks(t, cfg)

	client, serverSocketPath := runRepositoryService(t, cfg, nil, testserver.WithGitCommandFactory(gitCmdFactory))
	cfg.SocketPath = serverSocketPath

	repo, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])

	commitID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	correlationID := correlation.SafeRandomID()
	ctx = correlation.ContextWithCorrelation(ctx, correlationID)

	smudgeCfg := smudge.Config{
		GlRepository: gittest.GlRepository,
		Gitlab:       cfg.Gitlab,
		TLS:          cfg.TLS,
		DriverType:   smudge.DriverTypeProcess,
	}

	smudgeEnv, err := smudgeCfg.Environment()
	require.NoError(t, err)

	for _, tc := range []struct {
		desc            string
		includeLFSBlobs bool
		expectedEnv     []string
	}{
		{
			desc:            "without LFS blobs",
			includeLFSBlobs: false,
			expectedEnv: []string{
				"CORRELATION_ID=" + correlationID,
			},
		},
		{
			desc:            "with LFS blobs",
			includeLFSBlobs: true,
			expectedEnv: []string{
				"CORRELATION_ID=" + correlationID,
				smudgeEnv,
				"GITALY_LOG_DIR=" + cfg.Logging.Dir,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.GetArchive(ctx, &gitalypb.GetArchiveRequest{
				Repository:      repo,
				CommitId:        commitID,
				IncludeLfsBlobs: tc.includeLFSBlobs,
			})
			require.NoError(t, err)

			data, err := consumeArchive(stream)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.expectedEnv, strings.Split(text.ChompBytes(data), "\n"))
		})
	}
}

func compressedFileContents(t *testing.T, format gitalypb.GetArchiveRequest_Format, contents []byte) string {
	path := filepath.Join(testhelper.TempDir(t), "archive")
	require.NoError(t, os.WriteFile(path, contents, 0o644))

	switch format {
	case gitalypb.GetArchiveRequest_TAR:
		return text.ChompBytes(testhelper.MustRunCommand(t, nil, "tar", "tf", path))
	case gitalypb.GetArchiveRequest_TAR_GZ:
		return text.ChompBytes(testhelper.MustRunCommand(t, nil, "tar", "ztf", path))
	case gitalypb.GetArchiveRequest_TAR_BZ2:
		return text.ChompBytes(testhelper.MustRunCommand(t, nil, "tar", "jtf", path))
	case gitalypb.GetArchiveRequest_ZIP:
		return text.ChompBytes(testhelper.MustRunCommand(t, nil, "unzip", "-l", path))
	default:
		require.FailNow(t, "unsupported archive format: %v", format)
		return ""
	}
}

func consumeArchive(stream gitalypb.RepositoryService_GetArchiveClient) ([]byte, error) {
	reader := streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	})

	return io.ReadAll(reader)
}
