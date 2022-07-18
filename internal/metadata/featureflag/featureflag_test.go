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
		enabled     bool
		onByDefault bool
	}{
		{
			desc:        "empty name and no headers",
			flag:        "",
			headers:     nil,
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "no headers",
			flag:        "flag",
			headers:     nil,
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "no 'gitaly-feature' prefix in flag name",
			flag:        "flag",
			headers:     map[string]string{"flag": "true"},
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "not valid header value",
			flag:        "flag",
			headers:     map[string]string{"gitaly-feature-flag": "TRUE"},
			enabled:     false,
			onByDefault: false,
		},
		{
			desc:        "flag name with underscores",
			flag:        "flag_under_score",
			headers:     map[string]string{"gitaly-feature-flag-under-score": "true"},
			enabled:     true,
			onByDefault: false,
		},
		{
			desc:        "flag name with dashes",
			flag:        "flag-dash-ok",
			headers:     map[string]string{"gitaly-feature-flag-dash-ok": "true"},
			enabled:     true,
			onByDefault: false,
		},
		{
			desc:        "flag explicitly disabled",
			flag:        "flag",
			headers:     map[string]string{"gitaly-feature-flag": "false"},
			enabled:     false,
			onByDefault: true,
		},
		{
			desc:        "flag enabled by default but missing",
			flag:        "flag",
			headers:     map[string]string{},
			enabled:     true,
			onByDefault: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(createContext(), metadata.New(tc.headers))

			ff := FeatureFlag{tc.flag, tc.onByDefault}
			require.Equal(t, tc.enabled, ff.IsEnabled(ctx))
			require.Equal(t, tc.enabled, !ff.IsDisabled(ctx))
		})
	}
}
