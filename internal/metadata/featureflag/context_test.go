//go:build !gitaly_test_sha256

package featureflag

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	gitaly_metadata "gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"google.golang.org/grpc/metadata"
)

var (
	ffA = FeatureFlag{"feature-a", false}
	ffB = FeatureFlag{"feature-b", false}
)

//nolint:forbidigo // We cannot use `testhelper.Context()` given that it would inject feature flags
// already.
func createContext() context.Context {
	return context.Background()
}

func TestIncomingCtxWithFeatureFlag(t *testing.T) {
	ctx := createContext()
	require.False(t, ffA.IsEnabled(ctx))
	require.False(t, ffB.IsEnabled(ctx))

	t.Run("enabled", func(t *testing.T) {
		ctx := IncomingCtxWithFeatureFlag(ctx, ffA, true)
		require.True(t, ffA.IsEnabled(ctx))
	})

	t.Run("disabled", func(t *testing.T) {
		ctx := IncomingCtxWithFeatureFlag(ctx, ffA, false)
		require.False(t, ffA.IsEnabled(ctx))
	})

	t.Run("set multiple flags", func(t *testing.T) {
		ctxA := IncomingCtxWithFeatureFlag(ctx, ffA, true)
		ctxB := IncomingCtxWithFeatureFlag(ctxA, ffB, true)

		require.True(t, ffA.IsEnabled(ctxA))
		require.False(t, ffB.IsEnabled(ctxA))

		require.True(t, ffA.IsEnabled(ctxB))
		require.True(t, ffB.IsEnabled(ctxB))
	})
}

func TestOutgoingCtxWithFeatureFlag(t *testing.T) {
	ctx := createContext()
	require.False(t, ffA.IsEnabled(ctx))
	require.False(t, ffB.IsEnabled(ctx))

	t.Run("enabled", func(t *testing.T) {
		ctx := OutgoingCtxWithFeatureFlag(ctx, ffA, true)
		// The feature flag is only checked for incoming contexts, so it's not expected to
		// be enabled yet.
		require.False(t, ffA.IsEnabled(ctx))

		md, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok)

		// It should be enabled after converting it to an incoming context though.
		ctx = metadata.NewIncomingContext(createContext(), md)
		require.True(t, ffA.IsEnabled(ctx))
	})

	t.Run("disabled", func(t *testing.T) {
		ctx = OutgoingCtxWithFeatureFlag(ctx, ffA, false)
		require.False(t, ffA.IsEnabled(ctx))

		md, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok)

		ctx = metadata.NewIncomingContext(createContext(), md)
		require.False(t, ffA.IsEnabled(ctx))
	})

	t.Run("set multiple flags", func(t *testing.T) {
		ctxA := OutgoingCtxWithFeatureFlag(ctx, ffA, true)
		ctxB := OutgoingCtxWithFeatureFlag(ctxA, ffB, true)

		ctxA = gitaly_metadata.OutgoingToIncoming(ctxA)
		require.True(t, ffA.IsEnabled(ctxA))
		require.False(t, ffB.IsEnabled(ctxA))

		ctxB = gitaly_metadata.OutgoingToIncoming(ctxB)
		require.True(t, ffA.IsEnabled(ctxB))
		require.True(t, ffB.IsEnabled(ctxB))
	})
}

func TestGRPCMetadataFeatureFlag(t *testing.T) {
	testCases := []struct {
		flag        string
		headers     map[string]string
		enabled     bool
		onByDefault bool
		desc        string
	}{
		{"", nil, false, false, "empty name and no headers"},
		{"flag", nil, false, false, "no headers"},
		{"flag", map[string]string{"flag": "true"}, false, false, "no 'gitaly-feature' prefix in flag name"},
		{"flag", map[string]string{"gitaly-feature-flag": "TRUE"}, false, false, "not valid header value"},
		{"flag_under_score", map[string]string{"gitaly-feature-flag-under-score": "true"}, true, false, "flag name with underscores"},
		{"flag-dash-ok", map[string]string{"gitaly-feature-flag-dash-ok": "true"}, true, false, "flag name with dashes"},
		{"flag", map[string]string{"gitaly-feature-flag": "false"}, false, true, "flag explicitly disabled"},
		{"flag", map[string]string{}, true, true, "flag enabled by default but missing"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			md := metadata.New(tc.headers)
			ctx := metadata.NewIncomingContext(createContext(), md)

			require.Equal(t, tc.enabled, FeatureFlag{tc.flag, tc.onByDefault}.IsEnabled(ctx))
		})
	}
}

func TestFromContext(t *testing.T) {
	defer func(old map[string]FeatureFlag) {
		flagsByName = old
	}(flagsByName)

	defaultDisabledFlag := FeatureFlag{
		Name:        "default_disabled",
		OnByDefault: false,
	}

	defaultEnabledFlag := FeatureFlag{
		Name:        "default_enabled",
		OnByDefault: true,
	}

	flagsByName = map[string]FeatureFlag{
		"default_disabled": defaultDisabledFlag,
		"default_enabled":  defaultEnabledFlag,
	}

	t.Run("with no defined flags", func(t *testing.T) {
		require.Empty(t, FromContext(createContext()))
	})

	t.Run("with single defined flag", func(t *testing.T) {
		ctx := ContextWithFeatureFlag(createContext(), defaultDisabledFlag, false)
		ctx = ContextWithFeatureFlag(ctx, defaultEnabledFlag, true)

		require.Equal(t, map[FeatureFlag]bool{
			defaultDisabledFlag: false,
			defaultEnabledFlag:  true,
		}, FromContext(ctx))
	})

	t.Run("with defined flags and non-default values", func(t *testing.T) {
		ctx := ContextWithFeatureFlag(createContext(), defaultDisabledFlag, true)
		ctx = ContextWithFeatureFlag(ctx, defaultEnabledFlag, false)

		require.Equal(t, map[FeatureFlag]bool{
			defaultDisabledFlag: true,
			defaultEnabledFlag:  false,
		}, FromContext(ctx))
	})

	t.Run("with defined and undefined flag", func(t *testing.T) {
		undefinedFlag := FeatureFlag{
			Name: "undefined_flag",
		}

		ctx := ContextWithFeatureFlag(createContext(), defaultDisabledFlag, false)
		ctx = ContextWithFeatureFlag(ctx, undefinedFlag, false)

		require.Equal(t, map[FeatureFlag]bool{
			defaultDisabledFlag: false,
			undefinedFlag:       false,
		}, FromContext(ctx))
	})
}
