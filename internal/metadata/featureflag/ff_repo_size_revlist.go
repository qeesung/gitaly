package featureflag

// RevlistForRepoSize enables the RepositorySize RPC to use git rev-list to
// calculate the disk usage of the repository.
var RevlistForRepoSize = NewFeatureFlag("revlist_for_repo_size", false)
