package featureflag

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
)

const (
	// explicitFeatureFlagKey is used by ContextWithExplicitFeatureFlags to mark a context as
	// requiring all feature flags to have been explicitly defined.
	explicitFeatureFlagKey = "require_explicit_feature_flag_checks"
)

// ContextWithExplicitFeatureFlags marks the context such that all feature flags which are checked
// must have been explicitly set in that context. If a feature flag wasn't set to an explicit value,
// then checking this feature flag will panic. This is not for use in production systems, but is
// intended for tests to verify that we test each feature flag properly.
func ContextWithExplicitFeatureFlags(ctx context.Context) context.Context {
	return injectIntoIncomingAndOutgoingContext(ctx, explicitFeatureFlagKey, true)
}

// ContextWithFeatureFlag sets the feature flag in both the incoming and outgoing context.
func ContextWithFeatureFlag(ctx context.Context, flag FeatureFlag, enabled bool) context.Context {
	return injectIntoIncomingAndOutgoingContext(ctx, flag.MetadataKey(), enabled)
}

// OutgoingCtxWithFeatureFlag sets the feature flag for an outgoing context.
func OutgoingCtxWithFeatureFlag(ctx context.Context, flag FeatureFlag, enabled bool) context.Context {
	return outgoingCtxWithFeatureFlag(ctx, flag.MetadataKey(), enabled)
}

// OutgoingCtxWithRubyFeatureFlag sets the Ruby feature flag for an outgoing context.
func OutgoingCtxWithRubyFeatureFlag(ctx context.Context, flag FeatureFlag, enabled bool) context.Context {
	return outgoingCtxWithFeatureFlag(ctx, rubyHeaderKey(flag.Name), enabled)
}

func outgoingCtxWithFeatureFlag(ctx context.Context, key string, enabled bool) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}

	md = md.Copy()
	md.Set(key, strconv.FormatBool(enabled))

	return metadata.NewOutgoingContext(ctx, md)
}

// IncomingCtxWithFeatureFlag sets the feature flag for an incoming context. This is NOT meant for
// use in clients that transfer the context across process boundaries.
func IncomingCtxWithFeatureFlag(ctx context.Context, flag FeatureFlag, enabled bool) context.Context {
	return incomingCtxWithFeatureFlag(ctx, flag.MetadataKey(), enabled)
}

// IncomingCtxWithRubyFeatureFlag sets the Ruby feature flag for an incoming context. This is NOT
// meant for use in clients that transfer the context across process boundaries.
func IncomingCtxWithRubyFeatureFlag(ctx context.Context, flag FeatureFlag, enabled bool) context.Context {
	return incomingCtxWithFeatureFlag(ctx, rubyHeaderKey(flag.Name), enabled)
}

func incomingCtxWithFeatureFlag(ctx context.Context, key string, enabled bool) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	}

	md = md.Copy()
	md.Set(key, strconv.FormatBool(enabled))

	return metadata.NewIncomingContext(ctx, md)
}

func injectIntoIncomingAndOutgoingContext(ctx context.Context, key string, enabled bool) context.Context {
	incomingMD, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		incomingMD = metadata.New(map[string]string{})
	}

	incomingMD.Set(key, strconv.FormatBool(enabled))

	ctx = metadata.NewIncomingContext(ctx, incomingMD)

	return metadata.AppendToOutgoingContext(ctx, key, strconv.FormatBool(enabled))
}

// FromContext returns the set of all feature flags defined in the context. This returns both
// feature flags that are currently defined by Gitaly, but may also return some that aren't defined
// by us in case they match the feature flag prefix but don't have a definition. This function also
// returns the state of the feature flag *as defined in the context*. This value may be overridden.
func FromContext(ctx context.Context) map[FeatureFlag]bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return map[FeatureFlag]bool{}
	}

	flags := map[FeatureFlag]bool{}
	for metadataName, values := range md {
		if len(values) == 0 {
			continue
		}

		flag, err := FromMetadataKey(metadataName)
		if err != nil {
			continue
		}

		flags[flag] = values[0] == "true"
	}

	return flags
}

func rubyHeaderKey(flag string) string {
	return fmt.Sprintf("gitaly-feature-ruby-%s", strings.ReplaceAll(flag, "_", "-"))
}
