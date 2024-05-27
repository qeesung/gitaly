package featureflag

// SetTreeInAttrTreeConfig enables the use of
var SetTreeInAttrTreeConfig = NewFeatureFlag(
	"use_empty_tree_in_attr_tree_config",
	"v17.1.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/6064",
	false,
)
