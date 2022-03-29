package featureflag

// UserSquashQuarantinedVoting enables the use of a quarantine directory to stage all objects
// created by UserSquash into a temporary directory. This quarantine directory will only be migrated
// into the final repository when the RPC is successful, including a new transactional vote on the
// object ID of the resulting squashed commit.
var UserSquashQuarantinedVoting = NewFeatureFlag("user_squash_quarantined_voting", true)
