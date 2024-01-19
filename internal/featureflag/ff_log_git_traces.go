package featureflag

// LogGitTraces enables the collection of distributed traces via the git trace 2 API
var LogGitTraces = NewFeatureFlag(
	"log_git_traces",
	"v16.9.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5700",
	false,
)
