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
		return CapabilitiesGitConfig(ctx), errors.New("bundle-URI sink missing")
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
			Value: "any",
		},
		{
			Key:   fmt.Sprintf("bundle.%s.uri", defaultBundle),
			Value: uri,
		},
	}, nil
}
