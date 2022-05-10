package featureflag

// SquashUsingMerge uses merge to squash commits instead of rebase and commit-tree.
// It should be better at handling conflicts.
var SquashUsingMerge = NewFeatureFlag("squash_using_merge", false)
