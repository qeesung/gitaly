package featureflag

var AutocrlfConfig = NewFeatureFlag(
	"autocrlf_false",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4425",
	false,
)
