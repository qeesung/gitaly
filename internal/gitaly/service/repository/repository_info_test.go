package repository

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRepositoryInfo(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	writeFile := func(t *testing.T, byteCount int, pathComponents ...string) string {
		t.Helper()
		path := filepath.Join(pathComponents...)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), perm.PrivateDir))
		require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{0}, byteCount), perm.PrivateWriteOnceFile))
		return path
	}

	emptyRepoSize := func() uint64 {
		_, repoPath := gittest.CreateRepository(t, ctx, cfg)
		size, err := dirSizeInBytes(repoPath)
		require.NoError(t, err)
		return uint64(size)
	}()

	type setupData struct {
		request          *gitalypb.RepositoryInfoRequest
		expectedErr      error
		expectedResponse *gitalypb.RepositoryInfoResponse
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: nil,
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "invalid repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: &gitalypb.Repository{
							StorageName:  cfg.Storages[0].Name,
							RelativePath: "does/not/exist",
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "does/not/exist")),
					),
				}
			},
		},
		{
			desc: "empty repository",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects:    &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
					},
				}
			},
		},
		{
			desc: "repository with loose reference",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithReftable(t, "files backend specific test")

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				writeFile(t, 123, repoPath, "refs", "heads", "main")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size: emptyRepoSize + 123,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{
							LooseCount: 1,
						},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
					},
				}
			},
		},
		{
			desc: "repository with packed references",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithReftable(t, "files backend specific test")

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				writeFile(t, 123, repoPath, "packed-refs")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size: emptyRepoSize + 123,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{
							PackedSize: 123,
						},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
					},
				}
			},
		},
		{
			desc: "repository with loose blob",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oid := gittest.DefaultObjectHash.ZeroOID.String()
				writeFile(t, 123, repoPath, "objects", oid[0:2], oid[2:])

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 123,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:       123,
							RecentSize: 123,
						},
					},
				}
			},
		},
		{
			desc: "repository with stale loose object",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oid := gittest.DefaultObjectHash.ZeroOID.String()
				objectPath := writeFile(t, 123, repoPath, "objects", oid[0:2], oid[2:])
				stale := time.Now().Add(stats.StaleObjectsGracePeriod)
				require.NoError(t, os.Chtimes(objectPath, stale, stale))

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 123,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:      123,
							StaleSize: 123,
						},
					},
				}
			},
		},
		{
			desc: "repository with mixed stale and recent loose object",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				stale := time.Now().Add(stats.StaleObjectsGracePeriod)

				recentOID := gittest.DefaultObjectHash.ZeroOID.String()
				writeFile(t, 70, repoPath, "objects", recentOID[0:2], recentOID[2:])

				staleOID := strings.Repeat("1", len(recentOID))
				staleObjectPath := writeFile(t, 700, repoPath, "objects", staleOID[0:2], staleOID[2:])
				require.NoError(t, os.Chtimes(staleObjectPath, stale, stale))

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 70 + 700,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:       770,
							RecentSize: 70,
							StaleSize:  700,
						},
					},
				}
			},
		},
		{
			desc: "repository with packfile",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				writeFile(t, 123, repoPath, "objects", "pack", "pack-1234.pack")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 123,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:       123,
							RecentSize: 123,
						},
					},
				}
			},
		},
		{
			desc: "repository with cruft pack",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				writeFile(t, 123, repoPath, "objects", "pack", "pack-1234.pack")
				writeFile(t, 7, repoPath, "objects", "pack", "pack-1234.mtimes")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 123 + 7,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:      123,
							StaleSize: 123,
						},
					},
				}
			},
		},
		{
			desc: "repository with keep pack",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				writeFile(t, 123, repoPath, "objects", "pack", "pack-1234.pack")
				writeFile(t, 7, repoPath, "objects", "pack", "pack-1234.keep")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 123 + 7,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:       123,
							RecentSize: 123,
							KeepSize:   123,
						},
					},
				}
			},
		},
		{
			desc: "repository with different pack types",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				writeFile(t, 70, repoPath, "objects", "pack", "pack-1.pack")

				writeFile(t, 700, repoPath, "objects", "pack", "pack-2.pack")
				writeFile(t, 100, repoPath, "objects", "pack", "pack-2.keep")

				writeFile(t, 7000, repoPath, "objects", "pack", "pack-3.pack")
				writeFile(t, 1000, repoPath, "objects", "pack", "pack-3.mtimes")

				return setupData{
					request: &gitalypb.RepositoryInfoRequest{
						Repository: repo,
					},
					expectedResponse: &gitalypb.RepositoryInfoResponse{
						Size:       emptyRepoSize + 8000 + 800 + 70,
						References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
						Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
							Size:       7770,
							RecentSize: 770,
							KeepSize:   700,
							StaleSize:  7000,
						},
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			response, err := client.RepositoryInfo(ctx, setup.request)
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedResponse, response)
		})
	}
}

func TestConvertRepositoryInfo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc             string
		repoSize         uint64
		repoInfo         stats.RepositoryInfo
		expectedResponse *gitalypb.RepositoryInfoResponse
	}{
		{
			desc: "all-zero",
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
				Objects:    &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
			},
		},
		{
			desc:     "size",
			repoSize: 123,
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				Size:       123,
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
				Objects:    &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
			},
		},
		{
			desc: "references",
			repoInfo: stats.RepositoryInfo{
				References: stats.ReferencesInfo{
					ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
					LooseReferencesCount: 123,
					PackedReferencesSize: 456,
				},
			},
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{
					LooseCount: 123,
					PackedSize: 456,
				},
				Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{},
			},
		},
		{
			desc: "loose objects",
			repoInfo: stats.RepositoryInfo{
				LooseObjects: stats.LooseObjectsInfo{
					Size:      123,
					StaleSize: 3,
				},
			},
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
				Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
					Size:       123,
					RecentSize: 120,
					StaleSize:  3,
				},
			},
		},
		{
			desc: "packfiles",
			repoInfo: stats.RepositoryInfo{
				Packfiles: stats.PackfilesInfo{
					Size:      123,
					CruftSize: 3,
					KeepSize:  7,
				},
			},
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
				Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
					Size:       123,
					RecentSize: 120,
					StaleSize:  3,
					KeepSize:   7,
				},
			},
		},
		{
			desc: "loose objects and packfiles",
			repoInfo: stats.RepositoryInfo{
				LooseObjects: stats.LooseObjectsInfo{
					Size:      7,
					StaleSize: 3,
				},
				Packfiles: stats.PackfilesInfo{
					Size:      700,
					CruftSize: 300,
					KeepSize:  500,
				},
			},
			expectedResponse: &gitalypb.RepositoryInfoResponse{
				References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{},
				Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
					Size:       707,
					RecentSize: 404,
					StaleSize:  303,
					KeepSize:   500,
				},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			response := convertRepositoryInfo(tc.repoSize, tc.repoInfo)
			require.Equal(t, tc.expectedResponse, response)
		})
	}
}
