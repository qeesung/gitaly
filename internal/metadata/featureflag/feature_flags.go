package featureflag

type FeatureFlag interface {
	Name() string
	OnByDefault() bool
}

type RubyFeatureFlag struct {
	GoFeatureFlag
}

type GoFeatureFlag struct {
	name        string `json:"name"`
	onByDefault bool   `json:"on_by_default"`
}

func (g GoFeatureFlag) Name() string {
	return g.name
}

func (g GoFeatureFlag) OnByDefault() bool {
	return g.onByDefault
}

func NewGoFeatureFlag(name string, onByDefault bool) *GoFeatureFlag {
	return &GoFeatureFlag{
		name:        name,
		onByDefault: onByDefault,
	}
}

var (
	// GoUpdateHook will bypass the ruby update hook and use the go implementation of custom hooks
	GoUpdateHook = GoFeatureFlag{name: "go_update_hook", onByDefault: true}
	// RemoteBranchesLsRemote will use `ls-remote` for remote branches
	RemoteBranchesLsRemote = GoFeatureFlag{name: "ruby_remote_branches_ls_remote", onByDefault: true}
	// GoFetchSourceBranch enables a go implementation of FetchSourceBranch
	GoFetchSourceBranch = GoFeatureFlag{name: "go_fetch_source_branch", onByDefault: false}
	// DistributedReads allows praefect to redirect accessor operations to up-to-date secondaries
	DistributedReads = GoFeatureFlag{name: "distributed_reads", onByDefault: false}
	// GoPreReceiveHook will bypass the ruby pre-receive hook and use the go implementation
	GoPreReceiveHook = GoFeatureFlag{name: "go_prereceive_hook", onByDefault: true}
	// GoPostReceiveHook will bypass the ruby post-receive hook and use the go implementation
	GoPostReceiveHook = GoFeatureFlag{name: "go_postreceive_hook", onByDefault: false}
	// ReferenceTransactions will handle Git reference updates via the transaction service for strong consistency
	ReferenceTransactions = GoFeatureFlag{name: "reference_transactions", onByDefault: false}
	// ReferenceTransactionsOperationService will enable reference transactions for the OperationService
	ReferenceTransactionsOperationService = GoFeatureFlag{name: "reference_transactions_operation_service", onByDefault: true}
	// ReferenceTransactionsSmartHTTPService will enable reference transactions for the SmartHTTPService
	ReferenceTransactionsSmartHTTPService = GoFeatureFlag{name: "reference_transactions_smarthttp_service", onByDefault: true}
	// ReferenceTransactionsSSHService will enable reference transactions for the SSHService
	ReferenceTransactionsSSHService = GoFeatureFlag{name: "reference_transactions_ssh_service", onByDefault: true}
)

const (
	GoUpdateHookEnvVar      = "GITALY_GO_UPDATE"
	GoPreReceiveHookEnvVar  = "GITALY_GO_PRERECEIVE"
	GoPostReceiveHookEnvVar = "GITALY_GO_POSTRECEIVE"
)
