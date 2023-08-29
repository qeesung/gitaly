package featureflag

// GitV242 enables the use of Git v2.42.
var GitV242 = NewFeatureFlag(
	"git_v242",
	"v16.4.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5539",
	false,
)
