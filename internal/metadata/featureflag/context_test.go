package featureflag

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestIncomingCtxWithFeatureFlag(t *testing.T) {
	ctx := context.Background()
	require.False(t, IsEnabled(ctx, UseGitProtocolV2))

	ctx = IncomingCtxWithFeatureFlag(ctx, UseGitProtocolV2)
	require.True(t, IsEnabled(ctx, UseGitProtocolV2))
}

func TestOutgoingCtxWithFeatureFlag(t *testing.T) {
	ctx := context.Background()
	require.False(t, IsEnabled(ctx, UseGitProtocolV2))

	ctx = OutgoingCtxWithFeatureFlag(ctx, UseGitProtocolV2)
	require.False(t, IsEnabled(ctx, UseGitProtocolV2))

	// simulate an outgoing context leaving the process boundary and then
	// becoming an incoming context in a new process boundary
	md, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)

	ctx = metadata.NewIncomingContext(context.Background(), md)
	require.True(t, IsEnabled(ctx, UseGitProtocolV2))
}
