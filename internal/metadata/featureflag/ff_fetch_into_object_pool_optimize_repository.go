package featureflag

// FetchIntoObjectPoolOptimizeRepository will cause FetchIntoObjectPool to use OptimizeRepository to
// maintain the object pool instead of manually performing repository maintenance.
var FetchIntoObjectPoolOptimizeRepository = NewFeatureFlag(
	"fetch_into_object_pool_optimize_repository",
	"v15.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4342",
	false,
)
