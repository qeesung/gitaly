package featureflag

// FetchIntoObjectPoolPruneRefs enables pruning of references in object pools. This is required in
// order to fix cases where updating pools doesn't work anymore due to preexisting references
// conflicting with new references in the pool member.
var FetchIntoObjectPoolPruneRefs = NewFeatureFlag(
	"fetch_into_object_pool_prune_refs",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4394",
	true,
)
