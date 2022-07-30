package featureflag

// FindTagNotFound will return NotFound instead of Internal when the Git Tag
// doesn't exist.
var FindTagNotFound = NewFeatureFlag(
	"find_tag_not_found",
	"v15.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4366",
	false,
)
