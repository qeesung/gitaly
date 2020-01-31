package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"google.golang.org/grpc/metadata"
)

type fakeProtocolMessage struct {
	protocol string
}

func (f fakeProtocolMessage) GetGitProtocol() string {
	return f.protocol
}

func enableProtocolV2(ctx context.Context) context.Context {
	// TODO: replace this implementation with a helper function that deals
	// with the incoming context
	flag := featureflag.UseGitProtocolV2

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{featureflag.HeaderKey(flag): "true"})
	}
	md.Set(featureflag.HeaderKey(flag), "true")

	return metadata.NewIncomingContext(ctx, md)
}

func TestAddGitProtocolEnv(t *testing.T) {
	env := []string{"fake=value"}

	for _, tt := range []struct {
		desc string
		ctx  context.Context
		msg  fakeProtocolMessage
		env  []string
	}{
		{
			desc: "no feature flag with no V2 request",
			ctx:  context.Background(),
			env:  env,
		},
		{
			desc: "feature flag with no V2 request",
			ctx:  enableProtocolV2(context.Background()),
			env:  env,
		},
		{
			desc: "feature flag with V2 request",
			ctx:  enableProtocolV2(context.Background()),
			msg:  fakeProtocolMessage{protocol: "version=2"},
			env:  append(env, "GIT_PROTOCOL=version=2"),
		},
		{
			desc: "no feature flag with V2 request",
			ctx:  context.Background(),
			msg:  fakeProtocolMessage{protocol: "version=2"},
			env:  env,
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			actual := AddGitProtocolEnv(tt.ctx, tt.msg, env)
			require.Equal(t, tt.env, actual)
		})
	}
}
