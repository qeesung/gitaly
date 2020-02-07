package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
	// HooksRPC will invoke update, pre receive, and post receive hooks by using RPCs
	HooksRPC = "hooks_rpc"
	// CacheInvalidator controls the tracking of repo state via gRPC
	// annotations (i.e. accessor and mutator RPC's). This enables cache
	// invalidation by changing state when the repo is modified.
	CacheInvalidator = "cache_invalidator"
  // UseGitProtocolV2 enables support for git wire protocol v2
	UseGitProtocolV2 = "use_git_protocol_v2"
)

const (
	// HooksRPCEnvVar is the name of the environment variable we use to pass the feature flag down into gitaly-hooks
	HooksRPCEnvVar = "GITALY_HOOK_RPCS_ENABLED"
)
