package featureflag

// A set of feature flags used in Gitaly and Praefect.
// In order to support coverage of combined features usage all feature flags should be marked as enabled for the test.
// NOTE: if you add a new feature flag please add it to the `All` list defined below.
var (
	// GoSetConfig enables git2go implementation of SetConfig.
	GoSetConfig = FeatureFlag{Name: "go_set_config", OnByDefault: true}
	// PackObjectsHookWithSidechannel enables Unix socket sidechannels in 'gitaly-hooks git pack-objects'.
	PackObjectsHookWithSidechannel = FeatureFlag{Name: "pack_objects_hook_with_sidechannel", OnByDefault: false}
)

// All includes all feature flags.
var All = []FeatureFlag{
	GoSetConfig,
}
