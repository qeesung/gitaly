package featureflag

// RateLimit will enable the rate limiter to reject requests beyond a configured
// rate.
var RateLimit = NewFeatureFlag("rate_limit", false)
