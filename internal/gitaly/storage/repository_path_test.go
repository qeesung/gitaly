package storage_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestDeriveReplicaPath(t *testing.T) {
	require.Equal(t, "@cluster/repositories/6b/86/1", storage.DeriveReplicaPath(1))
	require.Equal(t, "@cluster/repositories/d4/73/2", storage.DeriveReplicaPath(2))
}

func TestDerivePoolPath(t *testing.T) {
	require.Equal(t, "@cluster/pools/6b/86/1", storage.DerivePoolPath(1))
	require.Equal(t, "@cluster/pools/d4/73/2", storage.DerivePoolPath(2))
}

func TestDeriveBlobOffloadPath(t *testing.T) {
	require.Equal(t, "@cluster/blob_offloads/6b/86/1", storage.DeriveBlobOffloadPath(1))
	require.Equal(t, "@cluster/blob_offloads/d4/73/2", storage.DeriveBlobOffloadPath(2))
}

func TestIsPoolRepository(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc               string
		repo               *gitalypb.Repository
		isPraefectPoolPath bool
		isRailsPoolPath    bool
	}{
		{
			desc:               "missing repository",
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
		{
			desc: "empty string",
			repo: &gitalypb.Repository{
				RelativePath: "",
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
		{
			desc: "praefect pool path",
			repo: &gitalypb.Repository{
				RelativePath: storage.DerivePoolPath(1),
			},
			isPraefectPoolPath: true,
			isRailsPoolPath:    false,
		},
		{
			desc: "praefect replica path",
			repo: &gitalypb.Repository{
				RelativePath: storage.DeriveReplicaPath(1),
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
		{
			desc: "praefect replica path with prefix",
			repo: &gitalypb.Repository{
				RelativePath: "prefix/" + storage.DeriveReplicaPath(1),
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
		{
			desc: "rails pool path",
			repo: &gitalypb.Repository{
				RelativePath: gittest.NewObjectPoolName(t),
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    true,
		},
		{
			desc: "rails pool path with prefix",
			repo: &gitalypb.Repository{
				RelativePath: "prefix/" + gittest.NewObjectPoolName(t),
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    true,
		},
		{
			desc: "rails path first to subdirs dont match full hash",
			repo: &gitalypb.Repository{
				RelativePath: "@pools/aa/bb/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff.git",
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
		{
			desc: "normal repos dont match",
			repo: &gitalypb.Repository{
				RelativePath: "@hashed/" + gittest.NewRepositoryName(t),
			},
			isPraefectPoolPath: false,
			isRailsPoolPath:    false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Run("Praefect", func(t *testing.T) {
				require.Equal(t, tc.isPraefectPoolPath, storage.IsPraefectPoolRepository(tc.repo))
			})

			t.Run("Rails", func(t *testing.T) {
				require.Equal(t, tc.isRailsPoolPath, storage.IsRailsPoolRepository(tc.repo))
			})

			t.Run("generic", func(t *testing.T) {
				require.Equal(t, tc.isRailsPoolPath || tc.isPraefectPoolPath, storage.IsPoolRepository(tc.repo))
			})
		})
	}
}

func TestBlobOffloadPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc               string
		repo               *gitalypb.Repository
		blobOffloadPath    string
	} {
		{
			desc:               "missing repository",
			blobOffloadPath: "",
		},
		{
			desc: "empty string list",
			repo: &gitalypb.Repository{
				GitAlternateObjectDirectories: []string{ },
			},
			blobOffloadPath: "",
		},
		{
			desc: "list with empty string",
			repo: &gitalypb.Repository{
				GitAlternateObjectDirectories: []string{ "" },
			},
			blobOffloadPath: "",
		},
		{
			desc: "blob offload path",
			repo: &gitalypb.Repository{
				GitAlternateObjectDirectories: []string{ storage.DeriveBlobOffloadPath(1) },
			},
			blobOffloadPath: storage.DeriveBlobOffloadPath(1),
		},
		{
			desc: "invalid blob offload path",
			repo: &gitalypb.Repository{
				GitAlternateObjectDirectories: []string{ storage.DerivePoolPath(1) },
			},
			blobOffloadPath: "",
		},
		{
			desc: "mixed valid and invalid blob offload path",
			repo: &gitalypb.Repository{
				GitAlternateObjectDirectories: []string{ storage.DerivePoolPath(1), "",
					storage.DeriveBlobOffloadPath(2), "" },
			},
			blobOffloadPath: storage.DeriveBlobOffloadPath(2),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.blobOffloadPath, storage.BlobOffloadPath(tc.repo))
		})
	}
}
