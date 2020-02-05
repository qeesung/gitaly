package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
)

type fakeProtocolMessage struct {
	protocol string
}

func (f fakeProtocolMessage) GetGitProtocol() string {
	return f.protocol
}

func enableProtocolV2(ctx context.Context) context.Context {
	return featureflag.IncomingCtxWithFeatureFlag(ctx, featureflag.UseGitProtocolV2)
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
