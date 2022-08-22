package featureflag

// PraefectGeneratedReplicaPaths will enable Praefect generated replica paths for new repositories.
var PraefectGeneratedReplicaPaths = NewFeatureFlag(
	"praefect_generated_replica_paths",
	"v15.0.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4218",
	true,
)
