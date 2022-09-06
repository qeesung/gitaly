package featureflag

// UseNewRepoSize will send the new repository size values in RepositorySize to
// the client.
var UseNewRepoSize = NewFeatureFlag(
	"use_new_repository_size",
	"v15.4.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4448",
	false,
)
