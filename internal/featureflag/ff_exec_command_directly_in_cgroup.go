package featureflag

// ExecCommandDirectlyInCgroup enables directly spawning commands in the correct Cgroup.
var ExecCommandDirectlyInCgroup = NewFeatureFlag(
	"exec_command_directly_in_cgroup",
	"v16.5.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5639",
	true,
)
