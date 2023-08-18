package featureflag

// UserRebaseToRefPureGit will enable the UserRebaseToRef RPC to
// use a pure git implemented rebase instead of git2go.
var UserRebaseToRefPureGit = NewFeatureFlag(
	"user_rebase_to_ref_pure_git",
	"v16.4.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5524",
	false,
)
