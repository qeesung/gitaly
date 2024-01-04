package featureflag

// ReturnStructuredErrorsInUserRevert enables return structured errors in UserRevert.
// Modify the RPC UserRevert to return structured errors instead of
// inline errors. Modify the handling of the following four
// errors: 'Conflict', 'Changes Already Applied', 'Branch diverged',
// and 'CustomHookError'. Returns the corresponding structured error.
var ReturnStructuredErrorsInUserRevert = NewFeatureFlag(
	"return_structured_errors_in_revert",
	"v16.8.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5752",
	false,
)
