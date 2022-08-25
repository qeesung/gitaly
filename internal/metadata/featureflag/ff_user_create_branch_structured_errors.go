package featureflag

// UserCreateBranchStructuredErrors will enable the use of structured errors in the UserCreateTag RPC.
var UserCreateBranchStructuredErrors = NewFeatureFlag(
	"user_create_branch_structured_errors",
	"v15.4.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4227",
	false,
)
