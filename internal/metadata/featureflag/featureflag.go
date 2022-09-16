package featureflag

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/env"
	"google.golang.org/grpc/metadata"
)

var (
	// EnableAllFeatureFlagsEnvVar will cause Gitaly to treat all feature flags as
	// enabled in case its value is set to `true`. Only used for testing purposes.
	EnableAllFeatureFlagsEnvVar = "GITALY_TESTING_ENABLE_ALL_FEATURE_FLAGS"

	// featureFlagsOverride allows to enable all feature flags with a
	// single environment variable. If the value of
	// GITALY_TESTING_ENABLE_ALL_FEATURE_FLAGS is set to "true", then all
	// feature flags will be enabled. This is only used for testing
	// purposes such that we can run integration tests with feature flags.
	featureFlagsOverride, _ = env.GetBool(EnableAllFeatureFlagsEnvVar, false)

	flagChecks = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_feature_flag_checks_total",
			Help: "Number of enabled/disabled checks for Gitaly server side feature flags",
		},
		[]string{"flag", "enabled"},
	)

	// flagsByName is the set of defined feature flags mapped by their respective name.
	flagsByName = map[string]FeatureFlag{}
)

// Feature flags must contain at least 2 characters. Can only contain lowercase letters,
// digits, and '_'. They must start with a letter, and cannot end with '_'.
// Feature flag name would be used to construct the corresponding metadata key, so:
//   - Only characters allowed by grpc metadata keys can be used and uppercase letters
//     would be normalized to lowercase, see
//     https://pkg.go.dev/google.golang.org/grpc/metadata#New
//   - It is critical that feature flags don't contain a dash, because the client converts
//     dashes to underscores when converting a feature flag's name to the metadata key,
//     and vice versa. The name wouldn't round-trip in case it had underscores and must
//     thus use dashes instead.
var ffNameRegexp = regexp.MustCompile(`^[a-z][a-z0-9_]*[a-z0-9]$`)

const (
	// ffPrefix is the prefix used for Gitaly-scoped feature flags.
	ffPrefix = "gitaly-feature-"
)

// DefinedFlags returns the set of feature flags that have been explicitly defined.
func DefinedFlags() []FeatureFlag {
	flags := make([]FeatureFlag, 0, len(flagsByName))
	for _, flag := range flagsByName {
		flags = append(flags, flag)
	}
	return flags
}

// FeatureFlag gates the implementation of new or changed functionality.
type FeatureFlag struct {
	// Name is the name of the feature flag.
	Name string `json:"name"`
	// OnByDefault is the default value if the feature flag is not explicitly set in
	// the incoming context.
	OnByDefault bool `json:"on_by_default"`
}

// NewFeatureFlag creates a new feature flag and adds it to the array of all existing feature flags.
// The name must be of the format `some_feature_flag`. Accepts a version and rollout issue URL as
// input that are not used for anything but only for the sake of linking to the feature flag rollout
// issue in the Gitaly project.
func NewFeatureFlag(name, version, rolloutIssueURL string, onByDefault bool) FeatureFlag {
	if !ffNameRegexp.MatchString(name) {
		panic("invalid feature flag name.")
	}

	featureFlag := FeatureFlag{
		Name:        name,
		OnByDefault: onByDefault,
	}

	flagsByName[name] = featureFlag

	return featureFlag
}

// FromMetadataKey parses the given gRPC metadata key into a Gitaly feature flag and performs the
// necessary conversions. Returns an error in case the metadata does not refer to a feature flag.
//
// This function tries to look up the default value via our set of flag definitions. In case the
// flag definition is unknown to Gitaly it assumes a default value of `false`.
func FromMetadataKey(metadataKey string) (FeatureFlag, error) {
	if !strings.HasPrefix(metadataKey, ffPrefix) {
		return FeatureFlag{}, fmt.Errorf("not a feature flag: %q", metadataKey)
	}

	flagName := strings.TrimPrefix(metadataKey, ffPrefix)
	flagName = strings.ReplaceAll(flagName, "-", "_")

	flag, ok := flagsByName[flagName]
	if !ok {
		flag = FeatureFlag{
			Name:        flagName,
			OnByDefault: false,
		}
	}

	return flag, nil
}

// FormatWithValue converts the feature flag into a string with the given state. Note that this
// function uses the feature flag name and not the raw metadata key as used in gRPC metadata.
func (ff FeatureFlag) FormatWithValue(enabled bool) string {
	return fmt.Sprintf("%s:%v", ff.Name, enabled)
}

// IsEnabled checks if the feature flag is enabled for the passed context.
// Only returns true if the metadata for the feature flag is set to "true"
func (ff FeatureFlag) IsEnabled(ctx context.Context) bool {
	if featureFlagsOverride {
		return true
	}

	val, ok := ff.valueFromContext(ctx)
	if !ok {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if _, ok := md[explicitFeatureFlagKey]; ok {
				panic(fmt.Sprintf("checking for feature %q without use of feature sets", ff.Name))
			}
		}

		return ff.OnByDefault
	}

	enabled := val == "true"

	flagChecks.WithLabelValues(ff.Name, strconv.FormatBool(enabled)).Inc()

	return enabled
}

// IsDisabled determines whether the feature flag is disabled in the incoming context.
func (ff FeatureFlag) IsDisabled(ctx context.Context) bool {
	return !ff.IsEnabled(ctx)
}

// MetadataKey returns the key of the feature flag as it is present in the metadata map.
func (ff FeatureFlag) MetadataKey() string {
	return ffPrefix + strings.ReplaceAll(ff.Name, "_", "-")
}

func (ff FeatureFlag) valueFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}

	val, ok := md[ff.MetadataKey()]
	if !ok {
		return "", false
	}

	if len(val) == 0 {
		return "", false
	}

	return val[0], true
}
