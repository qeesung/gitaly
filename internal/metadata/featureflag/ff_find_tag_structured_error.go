package featureflag

// FindTagStructuredError enables the use of structured errors for the FindTag RPC in case the tag
// could not be found.
var FindTagStructuredError = NewFeatureFlag(
	"find_tag_structured_error",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4398",
	false,
)
