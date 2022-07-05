package featureflag

// GitV2361Gl1 will enable use of Git v2.36.1.gl1.
var GitV2361Gl1 = NewFeatureFlag(
	"git_v2361gl1",
	"v15.0.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4194",
	false,
)
