package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
	// HooksRPC will invoke update, pre receive, and post receive hooks by using RPCs
	HooksRPC = "hooks_rpc"
	// GoFetchInternalRemote enables a go implementation of FetchInternalRemote
	GoFetchInternalRemote = "go_fetch_internal_remote"
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = "go_update_hook"
)

const (
	// HooksRPCEnvVar is the name of the environment variable we use to pass the feature flag down into gitaly-hooks
	HooksRPCEnvVar     = "GITALY_HOOK_RPCS_ENABLED"
	GoUpdateHookEnvVar = "GITALY_GO_UPDATE"
)
