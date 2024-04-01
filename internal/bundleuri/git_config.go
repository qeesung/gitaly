package bundleuri

import (
	"context"
	"errors"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// CapabilitiesGitConfig returns a slice of git.ConfigPairs that can be injected
// into the Git config to make it aware the bundle-URI capabilities are
// supported.
// This can be used when spawning git-upload-pack(1) --advertise-refs in
// response to the GET /info/refs request.
func CapabilitiesGitConfig(ctx context.Context) []git.ConfigPair {
	if featureflag.BundleURI.IsDisabled(ctx) {
		return []git.ConfigPair{}
	}

	return []git.ConfigPair{
		{
			Key:   "uploadpack.advertiseBundleURIs",
			Value: "true",
		},
	}
}

// UploadPackGitConfig return a slice of git.ConfigPairs you can inject into the
// call to git-upload-pack(1) to advertise the available bundle to the client
// who clones/fetches from the repository.
func UploadPackGitConfig(
	ctx context.Context,
	backupLocator backup.Locator,
	backupSink backup.Sink,
	repo storage.Repository,
) ([]git.ConfigPair, error) {
	if featureflag.BundleURI.IsDisabled(ctx) {
		return nil, nil
	}

	if backupLocator == nil {
		return nil, errors.New("backup locator missing")
	}

	if backupSink == nil {
		return nil, errors.New("backup sink missing")
	}
	theBackup, err := backupLocator.FindLatest(ctx, repo)
	if err != nil {
		return nil, err
	}

	for _, step := range theBackup.Steps {
		// Skip non-existing & incremental backups
		if len(step.BundlePath) == 0 || len(step.PreviousRefPath) > 0 {
			continue
		}

		uri, err := backupSink.SignedURL(ctx, step.BundlePath, 10*time.Minute)
		if err != nil {
			return nil, err
		}

		log.AddFields(ctx, log.Fields{"bundle_uri": true})

		return []git.ConfigPair{
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
				Value: uri,
			},
		}, nil
	}

	return nil, errors.New("no valid backup found")
}
