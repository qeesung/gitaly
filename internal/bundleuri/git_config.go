package bundleuri

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

// ErrSinkMissing indicates a sink is missing
var ErrSinkMissing = errors.New("bundle-URI sink missing")

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
	sink *Sink,
	repo storage.Repository,
) ([]git.ConfigPair, error) {
	if featureflag.BundleURI.IsDisabled(ctx) {
		return []git.ConfigPair{}, nil
	}

	if sink == nil {
		return CapabilitiesGitConfig(ctx), ErrSinkMissing
	}

	uri, err := sink.SignedURL(ctx, repo)
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
			Value: "all",
		},
		{
			Key:   "bundle.heuristic",
			Value: "creationToken",
		},
		{
			Key:   fmt.Sprintf("bundle.%s.uri", defaultBundle),
			Value: uri,
		},
		{
			// Gitaly uses only one bundle URI bundle, and it's a
			// bundle that contains the full history up to the point
			// when the bundle was generated. When the client is
			// enabled to download bundle URIs on git-fetch(1)
			// (see https://gitlab.com/gitlab-org/git/-/issues/271),
			// we want to avoid it to download that bundle, because
			// the client probably has most of the history already.
			//
			// Thus we set `creationToken` to a fixed value "1".
			// When Git fetches the bundle URI, it saved this value
			// to `fetch.bundleCreationToken` in the repository's
			// git config. The next time Git attempts to fetch
			// bundles, it ignores all bundles with `creationToken`
			// lower or equal to the saved value.
			Key:   fmt.Sprintf("bundle.%s.creationToken", defaultBundle),
			Value: "1",
		},
	}, nil
}
