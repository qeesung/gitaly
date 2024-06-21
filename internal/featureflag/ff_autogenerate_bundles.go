package featureflag

// AutogenerateBundlesForBundleURI enables the use of git's bundle URI feature
var AutogenerateBundlesForBundleURI = NewFeatureFlag(
	"autogenerate_bundles_for_bundleuri",
	"v17.1.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/6204",
	false,
)
