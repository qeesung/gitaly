package featureflag

// GoFindLicense enables Go implementation of FindLicense
var GoFindLicense = NewFeatureFlag(
	"go_find_license",
	"v14.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/3759",
	false,
)
