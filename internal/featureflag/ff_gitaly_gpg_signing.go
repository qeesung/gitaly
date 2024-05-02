package featureflag

// GPGSigning enables the use of Git v2.41.
var GPGSigning = NewFeatureFlag(
	"gpg_signing",
	"v16.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5361",
	true,
)
