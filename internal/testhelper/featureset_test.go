//go:build !gitaly_test_sha256

package testhelper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	ff "gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"google.golang.org/grpc/metadata"
)

var (
	featureFlagA = ff.FeatureFlag{Name: "test_feature_flag_a"}
	featureFlagB = ff.FeatureFlag{Name: "test_feature_flag_b"}
)

func TestNewFeatureSetsWithRubyFlags(t *testing.T) {
	testcases := []struct {
		desc         string
		features     []ff.FeatureFlag
		rubyFeatures []ff.FeatureFlag
		expected     FeatureSets
	}{
		{
			desc:     "single Go feature flag",
			features: []ff.FeatureFlag{featureFlagA},
			expected: FeatureSets{
				FeatureSet{
					features:     map[ff.FeatureFlag]bool{featureFlagA: false},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
				FeatureSet{
					features:     map[ff.FeatureFlag]bool{featureFlagA: true},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
			},
		},
		{
			desc:     "two Go feature flags",
			features: []ff.FeatureFlag{featureFlagA, featureFlagB},
			expected: FeatureSets{
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagA: false,
						featureFlagB: false,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagA: true,
						featureFlagB: false,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagA: false,
						featureFlagB: true,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagA: true,
						featureFlagB: true,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{},
				},
			},
		},
		{
			desc:         "single Ruby feature flag",
			rubyFeatures: []ff.FeatureFlag{featureFlagA},
			expected: FeatureSets{
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: false,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: true,
					},
				},
			},
		},
		{
			desc:         "two Ruby feature flags",
			rubyFeatures: []ff.FeatureFlag{featureFlagA, featureFlagB},
			expected: FeatureSets{
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: false,
						featureFlagB: false,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: true,
						featureFlagB: false,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: false,
						featureFlagB: true,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: true,
						featureFlagB: true,
					},
				},
			},
		},
		{
			desc:         "Go and Ruby feature flag",
			features:     []ff.FeatureFlag{featureFlagB},
			rubyFeatures: []ff.FeatureFlag{featureFlagA},
			expected: FeatureSets{
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagB: false,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: false,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagB: true,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: false,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagB: false,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: true,
					},
				},
				FeatureSet{
					features: map[ff.FeatureFlag]bool{
						featureFlagB: true,
					},
					rubyFeatures: map[ff.FeatureFlag]bool{
						featureFlagA: true,
					},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			featureSets := NewFeatureSetsWithRubyFlags(tc.features, tc.rubyFeatures)
			require.Equal(t, tc.expected, featureSets)
		})
	}
}

func TestFeatureSets_Run(t *testing.T) {
	// Define two default-enabled feature flags. Note that with `NewFeatureFlag()`, we
	// automatically add them to the list of defined feature flags. While this is stateful and
	// would theoretically also impact other tests, we don't really need to mind that given
	// that we use test-specific names for the flags here.
	featureFlagA := ff.NewFeatureFlag("global_feature_flag_a", "", "", true)
	featureFlagB := ff.NewFeatureFlag("global_feature_flag_b", "", "", true)

	var featureFlags [][2]bool
	NewFeatureSets(featureFlagB, featureFlagA).Run(t, func(t *testing.T, ctx context.Context) {
		incomingMD, ok := metadata.FromIncomingContext(ctx)
		require.True(t, ok)

		outgoingMD, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok)

		require.Equal(t, incomingMD, outgoingMD)

		featureFlags = append(featureFlags, [2]bool{
			featureFlagA.IsEnabled(ctx), featureFlagB.IsEnabled(ctx),
		})
	})

	require.Equal(t, [][2]bool{
		{false, false},
		{false, true},
		{true, false},
		{true, true},
	}, featureFlags)
}
