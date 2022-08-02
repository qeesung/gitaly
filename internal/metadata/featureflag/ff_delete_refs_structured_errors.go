package featureflag

// DeleteRefsStructuredErrors turns on metrics for pack objects
var DeleteRefsStructuredErrors = NewFeatureFlag(
	"delete_refs_structured_errors",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4348",
	false,
)
