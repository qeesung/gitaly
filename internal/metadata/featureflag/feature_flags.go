package featureflag

type FeatureFlag struct {
	Name        string `json:"name"`
	OnByDefault bool   `json:"on_by_default"`
}

var (
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = FeatureFlag{Name: "go_update_hook", OnByDefault: true}
	// RemoteBranchesLsRemote will use `ls-remote` for remote branches
	RemoteBranchesLsRemote = FeatureFlag{Name: "ruby_remote_branches_ls_remote", OnByDefault: true}
	// GoFetchSourceBranch enables a go implementation of FetchSourceBranch
	GoFetchSourceBranch = FeatureFlag{Name: "go_fetch_source_branch", OnByDefault: false}
	// DistributedReads allows praefect to redirect accessor operations to up-to-date secondaries
	DistributedReads = FeatureFlag{Name: "distributed_reads", OnByDefault: false}
	// GoPreReceiveHook will bypass the ruby pre-receive hook and use the go implementation
	GoPreReceiveHook = FeatureFlag{Name: "go_prereceive_hook", OnByDefault: true}
	// GoPostReceiveHook will bypass the ruby post-receive hook and use the go implementation
	GoPostReceiveHook = FeatureFlag{Name: "go_postreceive_hook", OnByDefault: false}
	// ReferenceTransactions will handle Git reference updates via the transaction service for strong consistency
	ReferenceTransactions = FeatureFlag{Name: "reference_transactions", OnByDefault: false}
	// ReferenceTransactionsOperationService will enable reference transactions for the OperationService
	ReferenceTransactionsOperationService = FeatureFlag{Name: "reference_transactions_operation_service", OnByDefault: true}
	// ReferenceTransactionsSmartHTTPService will enable reference transactions for the SmartHTTPService
	ReferenceTransactionsSmartHTTPService = FeatureFlag{Name: "reference_transactions_smarthttp_service", OnByDefault: true}
	// ReferenceTransactionsSSHService will enable reference transactions for the SSHService
	ReferenceTransactionsSSHService = FeatureFlag{Name: "reference_transactions_ssh_service", OnByDefault: true}
	// ReferenceTranasctiionsPrimaryWins will change transaction registration such that
	// secondaries will take part in transactions, but not influence their outcome.
	ReferenceTransactionsPrimaryWins = FeatureFlag{Name: "reference_transactions_primary_wins", OnByDefault: false}
)

const (
	GoUpdateHookEnvVar      = "GITALY_GO_UPDATE"
	GoPreReceiveHookEnvVar  = "GITALY_GO_PRERECEIVE"
	GoPostReceiveHookEnvVar = "GITALY_GO_POSTRECEIVE"
)
