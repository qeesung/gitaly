package featureflag

// UserCreateTagStructuredErrors will enable the use of structured errors in the UserCreateTag RPC.
var UserCreateTagStructuredErrors = NewFeatureFlag(
	"user_create_tag_structured_errors",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4372",
	false,
)
