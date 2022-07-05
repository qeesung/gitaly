package featureflag

// CherryPickStructuredErrors enables the UserCherryPick RPC to return
// structured errors.
var CherryPickStructuredErrors = NewFeatureFlag(
	"cherry_pick_structured_errors",
	"v15.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4325",
	false,
)
