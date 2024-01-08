package featureflag

// ReturnStructuredErrorsInUserDeleteTag enables return structured errors in UserDeleteBranch.
// Modify the RPC UserDeleteBranch to return structured errors instead of
// inline errors. Modify the handling of the following four
// errors: 'Access Check', 'Reference Update', and 'CustomHookError'.
// Returns the corresponding structured error.

var ReturnStructuredErrorsInUserDeleteTag = NewFeatureFlag(
	"return_structured_errors_in_user_delete_tag",
	"v16.8.1",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5756",
	false,
)
