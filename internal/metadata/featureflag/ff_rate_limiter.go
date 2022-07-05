package featureflag

// RateLimit will enable the rate limiter to reject requests beyond a configured
// rate.
var RateLimit = NewFeatureFlag(
	"rate_limit",
	"v14.10.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4181",
	false,
)
