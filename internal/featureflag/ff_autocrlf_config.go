package featureflag

// AutocrlfConfig changes the default global git configuration of
// autocrlf from `input` to `false`. This results in Gitaly being
// completely agnostic of line endings.
var AutocrlfConfig = NewFeatureFlag(
	"autocrlf_false",
	"v16.10.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4425",
	false,
)
