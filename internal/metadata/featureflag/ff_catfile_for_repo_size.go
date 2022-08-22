package featureflag

// CatfileRepoSize will enable the rate limiter to reject requests beyond a configured
// rate.
var CatfileRepoSize = NewFeatureFlag(
	"catfile_repo_size",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4421",
	false,
)
