package featureflag

// AttrSource will set --attr-source=HEAD for every Git command
var AttrSource = NewFeatureFlag(
	"attr_source",
	"v16.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5500",
	false,
)
