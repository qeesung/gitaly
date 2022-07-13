package featureflag

// GitV2371Gl1 will enable use of Git v2.37.1.gl1.
var GitV2371Gl1 = NewFeatureFlag(
	"git_v2371gl1",
	"v15.0.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4194",
	false,
)
