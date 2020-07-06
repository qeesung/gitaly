package featureflag

type FeatureFlag struct {
	Name        string `json:"name"`
	OnByDefault bool   `json:"on_by_default"`
}

var (
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = FeatureFlag{Name: "linguist_file_count_stats", OnByDefault: false}
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = FeatureFlag{Name: "go_update_hook", OnByDefault: false}
	// RemoteBranchesLsRemote will use `ls-remote` for remote branches
	RemoteBranchesLsRemote = FeatureFlag{Name: "ruby_remote_branches_ls_remote", OnByDefault: true}
	// GoFetchSourceBranch enables a go implementation of FetchSourceBranch
	GoFetchSourceBranch = FeatureFlag{Name: "go_fetch_source_branch", OnByDefault: false}
	// ReferenceTransactions will handle Git reference updates via the transaction service for strong consistency
	ReferenceTransactions = FeatureFlag{Name: "reference_transactions", OnByDefault: false}
	// DistributedReads allows praefect to redirect accessor operations to up-to-date secondaries
	DistributedReads = FeatureFlag{Name: "distributed_reads", OnByDefault: false}
	// GoPreReceiveHook will bypass the ruby pre-receive hook and use the go implementation
	GoPreReceiveHook = FeatureFlag{Name: "go_prereceive_hook", OnByDefault: false}
)

const (
	GoUpdateHookEnvVar     = "GITALY_GO_UPDATE"
	GoPreReceiveHookEnvVar = "GITALY_GO_PRERECEIVE"
)
