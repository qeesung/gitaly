package featureflag

// TransactionalRestoreCustomHooks will use transactional voting in the
// RestoreCustomHooks RPC
var TransactionalRestoreCustomHooks = NewFeatureFlag("tx_restore_custom_hooks", false)
