package featureflag

// PackObjectsMetrics turns on metrics for pack objects
var PackObjectsMetrics = NewFeatureFlag(
	"pack_objects_metrics",
	"v15.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4336",
	false,
)
