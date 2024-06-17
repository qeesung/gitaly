package featureflag

// SymrefUpdate is used to handle the rollout of using symref-update
// subcommands. This is required because the new command uses a different
// voting hash and to stay backward compatible we need to ensure that
// all nodes are on the same version before enabling this.
//
// Note that the command itself will be released in Git 2.46.0, and hence
// can only be enabled once that Git version is set to be the minimum (or
// if the patches are backported).
var SymrefUpdate = NewFeatureFlag(
	"symref_update",
	"v17.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/6153",
	false,
)
