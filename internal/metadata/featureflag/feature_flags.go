package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
	// HooksRPC will invoke update, pre receive, and post receive hooks by using RPCs
	HooksRPC = "hooks_rpc"
	// GitalyRubyCallHookRPC will invoke update, pre receive, and post receive hooks from the operations service by using RPCs
	// note: there doesn't need to be a separate gitaly ruby feature flag name. The same featureflag string can be used for both the go code
	// and being passed into gitaly-ruby
	GitalyRubyCallHookRPC = "call_hook_rpc"
	// GoFetchInternalRemote enables a go implementation of FetchInternalRemote
	GoFetchInternalRemote = "go_fetch_internal_remote"
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = "go_update_hook"
	// RemoteBranchesLsRemote will use `ls-remote` for remote branches
	RemoteBranchesLsRemote = "ruby_remote_branches_ls_remote"
	// ReferenceTransactions will handle Git reference updates via the transaction service for strong consistency
	ReferenceTransactions = "reference_transactions"
	// DistributedReads allows praefect to redirect accessor operations to up-to-date secondaries
	DistributedReads = "distributed_reads"
)

const (
	// HooksRPCEnvVar is the name of the environment variable we use to pass the feature flag down into gitaly-hooks
	HooksRPCEnvVar     = "GITALY_HOOK_RPCS_ENABLED"
	GoUpdateHookEnvVar = "GITALY_GO_UPDATE"
)
