package featureflag

// GitV245 enables the use of Git v2.45
var GitV245 = NewFeatureFlag(
	"git_v245",
	"v17.0.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/6024",
	false,
)
