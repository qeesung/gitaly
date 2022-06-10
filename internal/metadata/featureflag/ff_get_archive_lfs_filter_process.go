package featureflag

// GetArchiveLfsFilterProcess enables the use of a long-running filter process to smudge LFS
// pointers to their contents.
var GetArchiveLfsFilterProcess = NewFeatureFlag("get_archive_lfs_filter_process", false)
