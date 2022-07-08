package featureflag

// GoLanguageStats flag enables getting CommitLanguages statistics written in
// Go.
var GoLanguageStats = NewFeatureFlag(
	"go_language_stats",
	"v15.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4254",
	false,
)
