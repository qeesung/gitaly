package featureflag

// SimplifyFindLocalBranchesResponse enables the simplification of FindLocalBranchesRespnose
// by adding the generic Branch type in the response
var SimplifyFindLocalBranchesResponse = NewFeatureFlag(
	"simplify_find_local_branches_response",
	"v15.4.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/1294",
	true,
)
