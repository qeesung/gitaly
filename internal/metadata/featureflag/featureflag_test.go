//go:build !gitaly_test_sha256

package featureflag

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestFeatureFlag_FromMetadataKey(t *testing.T) {
	defer func(old map[string]FeatureFlag) {
		flagsByName = old
	}(flagsByName)

	defaultEnabled := FeatureFlag{
		Name:        "default_enabled",
		OnByDefault: true,
	}
	defaultDisabled := FeatureFlag{
		Name:        "default_disabled",
		OnByDefault: false,
	}

	flagsByName = map[string]FeatureFlag{
		defaultEnabled.Name:  defaultEnabled,
		defaultDisabled.Name: defaultDisabled,
	}

	for _, tc := range []struct {
		desc         string
		metadataKey  string
		expectedErr  error
		expectedFlag FeatureFlag
	}{
		{
			desc:        "empty key",
			metadataKey: "",
			expectedErr: fmt.Errorf("not a feature flag: \"\""),
		},
		{
			desc:        "invalid prefix",
			metadataKey: "invalid-prefix",
			expectedErr: fmt.Errorf("not a feature flag: \"invalid-prefix\""),
		},
		{
			desc:         "default enabled flag",
			metadataKey:  defaultEnabled.MetadataKey(),
			expectedFlag: defaultEnabled,
		},
		{
			desc:         "default disabled flag",
			metadataKey:  defaultDisabled.MetadataKey(),
			expectedFlag: defaultDisabled,
		},
		{
			desc:        "undefined flag",
			metadataKey: "gitaly-feature-not-defined",
			expectedFlag: FeatureFlag{
				Name: "not_defined",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			flag, err := FromMetadataKey(tc.metadataKey)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedFlag, flag)
		})
	}
}

func TestFeatureFlag_enabled(t *testing.T) {
	for _, tc := range []struct {
		desc        string
		flag        string
		headers     map[string]string
		shouldPanic bool
		enabled     bool
		onByDefault bool
	}{
		{
			desc:        "empty name",
			flag:        "",
			shouldPanic: true,
		},
		{
			desc:        "flag name too short",
			flag:        "a",
			shouldPanic: true,
		},
		{
			desc:        "flag name start with number",
			flag:        "0_flag",
			shouldPanic: true,
		},
		{
			desc:        "flag name start with underscore",
			flag:        "_flag",
			shouldPanic: true,
		},
		{
			desc:        "flag name end with underscore",
			flag:        "flag_",
			shouldPanic: true,
		},
		{
			desc:        "flag name with uppercase",
			flag:        "Flag",
			shouldPanic: true,
		},
		{
			desc:        "flag name with dashes",
			flag:        "flag-with-dash",
			shouldPanic: true,
		},
		{
			desc:        "flag name with characters disallowed by grpc metadata key",
			flag:        "flag_with_colon:_and_slash/",
			shouldPanic: true,
		},
		{
			desc:        "flag name with underscores",
			flag:        "flag_under_score",
			headers:     map[string]string{"gitaly-feature-flag-under-score": "true"},
			shouldPanic: false,
			enabled:     true,
			onByDefault: false,
		},
		{
			desc:        "no headers",
			flag:        "flag",
			headers:     nil,
			shouldPanic: false,
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "no 'gitaly-feature' prefix in flag name",
			flag:        "flag",
			headers:     map[string]string{"flag": "true"},
			shouldPanic: false,
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "not valid header value",
			flag:        "flag",
			headers:     map[string]string{"gitaly-feature-flag": "TRUE"},
			shouldPanic: false,
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "flag explicitly disabled",
			flag:        "flag",
			headers:     map[string]string{"gitaly-feature-flag": "false"},
			shouldPanic: false,
			enabled:     false,
			onByDefault: true,
		},
		{
			desc:        "flag enabled by default but missing",
			flag:        "flag",
			headers:     map[string]string{},
			shouldPanic: false,
			enabled:     true,
			onByDefault: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(createContext(), metadata.New(tc.headers))

			var ff FeatureFlag
			ffInitFunc := func() { ff = NewFeatureFlag(tc.flag, "", "", tc.onByDefault) }
			if tc.shouldPanic {
				require.PanicsWithValue(t, "invalid feature flag name.", ffInitFunc)
				return
			}
			require.NotPanics(t, ffInitFunc)
			require.Equal(t, tc.enabled, ff.IsEnabled(ctx))
			require.Equal(t, tc.enabled, !ff.IsDisabled(ctx))
		})
	}
}
