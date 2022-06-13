package featureflag

// UserDeleteBranchStructuredErrors enables the use of structured errors for the UserDeleteBranch
// RPC.
var UserDeleteBranchStructuredErrors = NewFeatureFlag("user_delete_branch_structured_errors", false)
