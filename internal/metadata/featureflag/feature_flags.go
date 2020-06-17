package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = "go_update_hook"
	// RemoteBranchesLsRemote will use `ls-remote` for remote branches
	RemoteBranchesLsRemote = "ruby_remote_branches_ls_remote"
	// ReferenceTransactions will handle Git reference updates via the transaction service for strong consistency
	ReferenceTransactions = "reference_transactions"
	// DistributedReads allows praefect to redirect accessor operations to up-to-date secondaries
	DistributedReads = "distributed_reads"
	// GoPrereceiveHook will bypass the ruby pre-receive hook and use the go implementation
	GoPreReceiveHook = "go_prereceive_hook"
)

const (
	GoUpdateHookEnvVar     = "GITALY_GO_UPDATE"
	GoPreReceiveHookEnvVar = "GITALY_GO_PRERECEIVE"
)
