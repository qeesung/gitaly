package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestEnableWritesSubcommand(t *testing.T) {
	mockSvc := &mockPraefectInfoService{}
	ln, clean := StartPraefectInfoService(t, mockSvc)
	defer clean()

	type EnableWritesFunc func(context.Context, *gitalypb.EnableWritesRequest) (*gitalypb.EnableWritesResponse, error)
	for _, tc := range []struct {
		desc             string
		args             []string
		enableWritesFunc func(testing.TB) EnableWritesFunc
		error            error
	}{
		{
			desc:  "missing virtual-storage",
			args:  []string{},
			error: errMissingVirtualStorage,
		},
		{
			desc:  "unexpected positional arg",
			args:  []string{"-virtual-storage=passed-storage", "unexpected-positional-argument"},
			error: UnexpectedPositionalArgsError{Command: "enable-writes"},
		},
		{
			desc: "success",
			args: []string{"-virtual-storage=passed-storage"},
			enableWritesFunc: func(t testing.TB) EnableWritesFunc {
				return func(_ context.Context, req *gitalypb.EnableWritesRequest) (*gitalypb.EnableWritesResponse, error) {
					assert.Equal(t, &gitalypb.EnableWritesRequest{
						VirtualStorage: "passed-storage",
					}, req)
					return &gitalypb.EnableWritesResponse{}, nil
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			mockSvc.EnableWritesFunc = nil
			if tc.enableWritesFunc != nil {
				mockSvc.EnableWritesFunc = tc.enableWritesFunc(t)
			}

			cmd := &enableWritesSubcommand{}
			flags := cmd.FlagSet()
			require.NoError(t, flags.Parse(tc.args))
			require.Equal(t, tc.error, cmd.Exec(flags, config.Config{
				SocketPath: ln.Addr().String(),
			}))
		})
	}
}
