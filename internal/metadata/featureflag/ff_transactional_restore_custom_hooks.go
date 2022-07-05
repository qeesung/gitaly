package featureflag

// TransactionalRestoreCustomHooks will use transactional voting in the
// RestoreCustomHooks RPC
var TransactionalRestoreCustomHooks = NewFeatureFlag(
	"tx_restore_custom_hooks",
	"v15.0.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4203",
	false,
)
