package repository

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

var (
	secret       = "Magic secret"
	redirectPath = "/redirecting-snapshot.tar"
	tarPath      = "/snapshot.tar"
)

type tarTesthandler struct {
	tarData io.Reader
	secret  string
}

func (h *tarTesthandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != h.secret {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.RequestURI {
	case redirectPath:
		http.Redirect(w, r, tarPath, http.StatusFound)
	case tarPath:
		if _, err := io.Copy(w, h.tarData); err != nil {
			panic(err)
		}
	default:
		http.Error(w, "Not found", 404)
	}
}

// Create a tar file for the repo in memory, without relying on TarBuilder
func generateTarFile(t *testing.T, path string) ([]byte, []string) {
	var data []byte
	if runtime.GOOS == "darwin" {
		data = testhelper.MustRunCommand(t, nil, "tar", "-C", path, "--no-mac-metadata", "-cf", "-", ".")
	} else {
		data = testhelper.MustRunCommand(t, nil, "tar", "-C", path, "-cf", "-", ".")
	}

	entries, err := archive.TarEntries(bytes.NewReader(data))
	require.NoError(t, err)

	return data, entries
}

func TestCreateRepositoryFromSnapshot_success(t *testing.T) {
	if testhelper.IsWALEnabled() && gittest.DefaultObjectHash.Format == git.ObjectHashSHA256.Format {
		t.Skip(`
CreateRepositoryFromSnapshot is broken with SHA256 but the test failures only surface with transactions
enabled. For more details, see: https://gitlab.com/gitlab-org/gitaly/-/issues/5662`)
	}

	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch))

	// Ensure these won't be in the archive
	require.NoError(t, os.Remove(filepath.Join(sourceRepoPath, "config")))
	require.NoError(t, os.RemoveAll(filepath.Join(sourceRepoPath, "hooks")))

	data, entries := generateTarFile(t, sourceRepoPath)

	// Create a HTTP server that serves a given tar file
	srv := httptest.NewServer(&tarTesthandler{tarData: bytes.NewReader(data), secret: secret})
	defer srv.Close()

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: gittest.NewRepositoryName(t),
	}

	response, err := client.CreateRepositoryFromSnapshot(ctx, &gitalypb.CreateRepositoryFromSnapshotRequest{
		Repository: repo,
		HttpUrl:    srv.URL + tarPath,
		HttpAuth:   secret,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, response, &gitalypb.CreateRepositoryFromSnapshotResponse{})

	repoAbsolutePath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
	require.DirExists(t, repoAbsolutePath)
	for _, entry := range entries {
		if testhelper.IsWALEnabled() {
			// The transaction manager repacks the objects from the snapshot into a single pack file.
			// Skip asserting the exact objects on the disk and assert afterwards that the objects
			// from the source repo exist in the target repo.
			switch {
			case strings.HasPrefix(entry, "./objects"):
				continue
			}
		}

		if strings.HasSuffix(entry, "/") {
			require.DirExists(t, filepath.Join(repoAbsolutePath, entry), "directory %q not unpacked", entry)
		} else {
			require.FileExists(t, filepath.Join(repoAbsolutePath, entry), "file %q not unpacked", entry)
		}
	}

	// hooks/ and config were excluded, but the RPC should create them
	require.FileExists(t, filepath.Join(repoAbsolutePath, "config"), "Config file not created")

	targetPath, err := config.NewLocator(cfg).GetRepoPath(ctx, gittest.RewrittenRepository(t, ctx, cfg, repo))
	require.NoError(t, err)

	require.ElementsMatch(t,
		gittest.ListObjects(t, cfg, sourceRepoPath),
		gittest.ListObjects(t, cfg, targetPath),
	)
}

func TestCreateRepositoryFromSnapshot_repositoryExists(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	// This creates the first repository on the server. As this test can run with Praefect in front of it,
	// we'll use the next replica path Praefect will assign in order to ensure this repository creation
	// conflicts even with Praefect in front of it.
	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		RelativePath: storage.DeriveReplicaPath(1),
	})

	response, err := client.CreateRepositoryFromSnapshot(ctx, &gitalypb.CreateRepositoryFromSnapshotRequest{
		Repository: repo,
	})
	testhelper.RequireGrpcCode(t, err, codes.AlreadyExists)
	if testhelper.IsPraefectEnabled() {
		require.Contains(t, err.Error(), "route repository creation: reserve repository id: repository already exists")
	} else {
		require.Contains(t, err.Error(), "creating repository: repository exists already")
	}
	require.Nil(t, response)
}

func TestCreateRepositoryFromSnapshot_invalidArguments(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)
	srv := httptest.NewServer(&tarTesthandler{secret: secret})
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		desc                 string
		url                  string
		auth                 string
		resolvedAddress      string
		expectedCode         codes.Code
		expectedErrSubstring string
	}{
		{
			desc:                 "http bad auth",
			url:                  srv.URL + tarPath,
			auth:                 "Bad authentication",
			expectedCode:         codes.Internal,
			expectedErrSubstring: "HTTP server: 401 ",
		},
		{
			desc:                 "http not found",
			url:                  srv.URL + tarPath + ".does-not-exist",
			auth:                 secret,
			expectedCode:         codes.Internal,
			expectedErrSubstring: "HTTP server: 404 ",
		},
		{
			desc:                 "http do not follow redirects",
			url:                  srv.URL + redirectPath,
			auth:                 secret,
			expectedCode:         codes.Internal,
			expectedErrSubstring: "HTTP server: 302 ",
		},
		{
			desc:                 "resolved address not in expected format",
			url:                  srv.URL + tarPath,
			auth:                 secret,
			resolvedAddress:      "foo/bar",
			expectedCode:         codes.InvalidArgument,
			expectedErrSubstring: "creating resolved HTTP client: invalid resolved address",
		},
		{
			desc:                 "invalid URL",
			url:                  "invalid!scheme://invalid.invalid",
			auth:                 secret,
			expectedCode:         codes.InvalidArgument,
			expectedErrSubstring: "Bad HTTP URL",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			response, err := client.CreateRepositoryFromSnapshot(ctx, &gitalypb.CreateRepositoryFromSnapshotRequest{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: gittest.NewRepositoryName(t),
				},
				HttpUrl:         tc.url,
				HttpAuth:        tc.auth,
				ResolvedAddress: tc.resolvedAddress,
			})
			testhelper.RequireGrpcCode(t, err, tc.expectedCode)
			require.Nil(t, response)
			require.Contains(t, err.Error(), tc.expectedErrSubstring)
		})
	}
}

func TestCreateRepositoryFromSnapshot_malformedArchive(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	// Note that we are also writing a blob here that is 16kB in size. This is because there is a bug in
	// CreateRepositoryFromSnapshot that would cause it to succeed when a tiny archive gets truncated. Please refer
	// to https://gitlab.com/gitlab-org/gitaly/-/issues/5503.
	_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "blob", Mode: "100644", Content: string(incompressibleData(16 * 1024))},
	))

	data, _ := generateTarFile(t, sourceRepoPath)
	// Only serve half of the tar file
	dataReader := io.LimitReader(bytes.NewReader(data), int64(len(data)/2))

	srv := httptest.NewServer(&tarTesthandler{tarData: dataReader, secret: secret})
	defer srv.Close()

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: gittest.NewRepositoryName(t),
	}
	repoPath, err := config.NewLocator(cfg).GetRepoPath(ctx, repo, storage.WithRepositoryVerificationSkipped())
	require.NoError(t, err)

	response, err := client.CreateRepositoryFromSnapshot(ctx, &gitalypb.CreateRepositoryFromSnapshotRequest{
		Repository: repo,
		HttpUrl:    srv.URL + tarPath,
		HttpAuth:   secret,
	})
	require.Error(t, err)
	require.Nil(t, response)

	// Ensure that a partial result is not left in place
	require.NoFileExists(t, repoPath)
}

func TestCreateRepositoryFromSnapshot_resolvedAddressSuccess(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	_, sourceRepoPath := gittest.CreateRepository(t, ctx, cfg)

	// Ensure these won't be in the archive
	require.NoError(t, os.Remove(filepath.Join(sourceRepoPath, "config")))
	require.NoError(t, os.RemoveAll(filepath.Join(sourceRepoPath, "hooks")))

	data, entries := generateTarFile(t, sourceRepoPath)

	// Create a HTTP server that serves a given tar file
	srv := httptest.NewServer(&tarTesthandler{tarData: bytes.NewReader(data), secret: secret})
	defer srv.Close()

	repoRelativePath := gittest.NewRepositoryName(t)

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: repoRelativePath,
	}

	// Any URL should be resolved to the provided IP in resolved address
	// so provide a random URL and it should work as long as the resolved
	// address is correct (here we're utilizing the fact that HTTP doesn't
	// have SSL verification).
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	randomHostname := fmt.Sprintf("http://localhost:%s%s", u.Port(), tarPath)

	response, err := client.CreateRepositoryFromSnapshot(ctx, &gitalypb.CreateRepositoryFromSnapshotRequest{
		Repository:      repo,
		HttpUrl:         randomHostname,
		HttpAuth:        secret,
		ResolvedAddress: u.Hostname(),
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, response, &gitalypb.CreateRepositoryFromSnapshotResponse{})

	repoAbsolutePath := filepath.Join(cfg.Storages[0].Path, gittest.GetReplicaPath(t, ctx, cfg, repo))
	require.DirExists(t, repoAbsolutePath)
	for _, entry := range entries {
		if strings.HasSuffix(entry, "/") {
			require.DirExists(t, filepath.Join(repoAbsolutePath, entry), "directory %q not unpacked", entry)
		} else {
			require.FileExists(t, filepath.Join(repoAbsolutePath, entry), "file %q not unpacked", entry)
		}
	}

	// hooks/ and config were excluded, but the RPC should create them
	require.FileExists(t, filepath.Join(repoAbsolutePath, "config"), "Config file not created")
}

func TestServer_CreateRepositoryFromSnapshot_validate(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, client := setupRepositoryService(t)

	testCases := []struct {
		desc        string
		req         *gitalypb.CreateRepositoryFromSnapshotRequest
		expectedErr error
	}{
		{
			desc:        "no repository provided",
			req:         &gitalypb.CreateRepositoryFromSnapshotRequest{Repository: nil},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.CreateRepositoryFromSnapshot(ctx, tc.req)
			require.Error(t, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
