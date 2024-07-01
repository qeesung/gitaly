package featureflag

// TransactionProcReceive enables execution of the proc-receive hook during transaction. The
// proc-receive hook handles performing reference updates and committing the transaction before
// signaling back to the client that the updates were successful.
var TransactionProcReceive = NewFeatureFlag(
	"transaction_proc_receive",
	"v17.2.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/6175",
	false,
)
