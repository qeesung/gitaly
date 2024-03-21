package featureflag

// GitV244 enables the use of Git v2.44
var GitV244 = NewFeatureFlag(
	"git_v244",
	"v16.11.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5906",
	false,
)
