package featureflag

// UseUnifiedGetTreeEntries enables the use of the new unified code paths for the `GetTreeEntries`
// RPC. When this flag is enabled, the RPC will call the `sendTreeEntriesUnified()` function which
// has the following differences to the previous `sendTreeEntries()` function:
//  1. repo.ReadTree() is used in both the recursive and non-recursive case. `catfile.TreeEntries`
//     is no longer used.
//  2. Structured errors returned for invalid or non-existent revisions and paths are slightly
//     tweaked to be more semantically correct.
//  3. An error is returned if the provided path is not treeish. Previously this simply resulted
//     in an empty result set.
var UseUnifiedGetTreeEntries = NewFeatureFlag(
	"use_unified_get_tree_entries",
	"v16.10.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/5829",
	false,
)
