package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// FilterShasWithSignaturesGo will cause the FilterShasWithSignatures RPC to use the go implementation when set
	FilterShasWithSignaturesGo = "filter_shas_with_signatures_go"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
	// HooksRPC will invoke update, pre receive, and post receive hooks by using RPCs
	HooksRPC = "hooks_rpc"
)

const (
	// HooksRPCEnvVar is the name of the environment variable we use to pass the feature flag down into gitaly-hooks
	HooksRPCEnvVar = "GITALY_HOOK_RPCS_ENABLED"
)
