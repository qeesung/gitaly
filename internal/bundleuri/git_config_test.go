package bundleuri

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestUploadPackGitConfig(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.BundleURI).
		Run(t, testUploadPackGitConfig)
}

func testUploadPackGitConfig(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc           string
		findLatestFunc func(context.Context, storage.Repository) (*backup.Backup, error)
		signedURLFunc  func(context.Context, string, time.Duration) (string, error)
		expectedConfig []git.ConfigPair
	}{
		{
			desc: "no backup locator nor sink",
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "no backup locator",
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "", structerr.NewNotFound("not signed")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "no backup sink",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return nil, structerr.NewNotFound("no backup found")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "no backup found",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return nil, structerr.NewNotFound("no backup found")
			},
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "", structerr.NewNotFound("not signed")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "backup has no steps",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return &backup.Backup{}, nil
			},
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "", structerr.NewNotFound("not signed")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "backup step is incremental",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return &backup.Backup{
					Steps: []backup.Step{{PreviousRefPath: "not-nil"}},
				}, nil
			},
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "", structerr.NewNotFound("not signed")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "sign failed",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return &backup.Backup{
					Steps: []backup.Step{{BundlePath: "not-nil"}},
				}, nil
			},
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "", structerr.NewNotFound("not signed")
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
			},
		},
		{
			desc: "success",
			findLatestFunc: func(context.Context, storage.Repository) (*backup.Backup, error) {
				return &backup.Backup{
					Steps: []backup.Step{{BundlePath: "not-nil"}},
				}, nil
			},
			signedURLFunc: func(context.Context, string, time.Duration) (string, error) {
				return "https://example.com/bundle.git?signed=ok", nil
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
				{
					Key:   "bundle.version",
					Value: "1",
				},
				{
					Key:   "bundle.mode",
					Value: "any",
				},
				{
					Key:   "bundle.some.uri",
					Value: "https://example.com/bundle.git?signed=ok",
				},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			var locator backup.Locator
			if tc.findLatestFunc != nil {
				locator = dummyLocator{findLatestFunc: tc.findLatestFunc}
			}

			var sink backup.Sink
			if tc.signedURLFunc != nil {
				sink = dummySink{signedURLFunc: tc.signedURLFunc}
			}

			actual := UploadPackGitConfig(ctx, locator, sink, repo)

			if featureflag.BundleURI.IsEnabled(ctx) && tc.expectedConfig != nil {
				require.Equal(t, tc.expectedConfig, actual)
			} else {
				require.Empty(t, actual)
			}
		})
	}
}

type dummyLocator struct {
	findLatestFunc func(context.Context, storage.Repository) (*backup.Backup, error)
}

// BeginFull is not supported by this dummyLocator.
func (l dummyLocator) BeginFull(ctx context.Context, repo storage.Repository, backupID string) *backup.Backup {
	return nil
}

// BeginIncremental is not supported by this dummyLocator.
func (l dummyLocator) BeginIncremental(ctx context.Context, repo storage.Repository, backupID string) (*backup.Backup, error) {
	return nil, structerr.NewUnimplemented("BeginIncremental not implemented for dummyLocator")
}

// Commit is not supported by this dummyLocator.
func (l dummyLocator) Commit(ctx context.Context, backup *backup.Backup) error {
	return structerr.NewUnimplemented("Commit not implemented for dummyLocator")
}

// FindLatest calls the findLatestFunc of the dummyLocator.
func (l dummyLocator) FindLatest(ctx context.Context, repo storage.Repository) (*backup.Backup, error) {
	return l.findLatestFunc(ctx, repo)
}

// Find is not supported by this dummyLocator.
func (l dummyLocator) Find(ctx context.Context, repo storage.Repository, backupID string) (*backup.Backup, error) {
	return nil, structerr.NewUnimplemented("Find not implemented for dummyLocator")
}

type dummySink struct {
	signedURLFunc func(context.Context, string, time.Duration) (string, error)
}

// Close the dummySink.
func (s dummySink) Close() error {
	return nil
}

// GetWriter is not supported by this dummySink.
func (s dummySink) GetWriter(ctx context.Context, relativePath string) (io.WriteCloser, error) {
	return nil, structerr.NewUnimplemented("GetWriter not implemented for dummySink")
}

// GetReader is not supported by this dummySink.
func (s dummySink) GetReader(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	return nil, structerr.NewUnimplemented("GetReader not implemented for dummySink")
}

// SignedURL calls the signedURLFunc of the dummySink.
func (s dummySink) SignedURL(ctx context.Context, relativePath string, expiry time.Duration) (string, error) {
	return s.signedURLFunc(ctx, relativePath, expiry)
}
