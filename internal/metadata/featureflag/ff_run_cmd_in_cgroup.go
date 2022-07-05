package featureflag

// RunCommandsInCGroup allows all commands to be run within a cgroup
var RunCommandsInCGroup = NewFeatureFlag(
	"run_cmds_in_cgroup",
	"v14.10.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4102",
	false,
)
