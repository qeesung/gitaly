package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type mockPraefectInfoService struct {
	gitalypb.UnimplementedPraefectInfoServiceServer
	DatalossCheckFunc func(context.Context, *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error)
	EnableWritesFunc  func(context.Context, *gitalypb.EnableWritesRequest) (*gitalypb.EnableWritesResponse, error)
}

func (m mockPraefectInfoService) DatalossCheck(ctx context.Context, r *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
	return m.DatalossCheckFunc(ctx, r)
}

func (m mockPraefectInfoService) EnableWrites(ctx context.Context, r *gitalypb.EnableWritesRequest) (*gitalypb.EnableWritesResponse, error) {
	return m.EnableWritesFunc(ctx, r)
}

func TestDatalossSubcommand(t *testing.T) {
	mockSvc := &mockPraefectInfoService{}
	ln, clean := listenAndServe(t, []svcRegistrar{registerPraefectInfoServer(mockSvc)})
	defer clean()
	for _, tc := range []struct {
		desc            string
		args            []string
		virtualStorages []*config.VirtualStorage
		datalossCheck   func(context.Context, *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error)
		output          string
		error           error
	}{
		{
			desc:  "positional arguments",
			args:  []string{"-virtual-storage=test-virtual-storage", "positional-arg"},
			error: UnexpectedPositionalArgsError{Command: "dataloss"},
		},
		{
			desc: "no failover",
			args: []string{"-virtual-storage=test-virtual-storage"},
			datalossCheck: func(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				assert.Equal(t, "test-virtual-storage", req.GetVirtualStorage())
				return &gitalypb.DatalossCheckResponse{
					CurrentPrimary: "test-current-primary",
				}, nil
			},
			output: `Virtual storage: test-virtual-storage
  Current write-enabled primary: test-current-primary
    No data loss as the virtual storage has not encountered a failover
`,
		},
		{
			desc: "no data loss",
			args: []string{"-virtual-storage=test-virtual-storage"},
			datalossCheck: func(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				assert.Equal(t, "test-virtual-storage", req.GetVirtualStorage())
				return &gitalypb.DatalossCheckResponse{
					PreviousWritablePrimary: "test-previous-primary",
					IsReadOnly:              false,
					CurrentPrimary:          "test-current-primary",
				}, nil
			},
			output: `Virtual storage: test-virtual-storage
  Current write-enabled primary: test-current-primary
  Previous write-enabled primary: test-previous-primary
    No data loss from failing over from test-previous-primary
`,
		},
		{
			desc: "data loss",
			args: []string{"-virtual-storage=test-virtual-storage"},
			datalossCheck: func(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				assert.Equal(t, "test-virtual-storage", req.GetVirtualStorage())
				return &gitalypb.DatalossCheckResponse{
					PreviousWritablePrimary: "test-previous-primary",
					IsReadOnly:              true,
					CurrentPrimary:          "test-current-primary",
					OutdatedNodes: []*gitalypb.DatalossCheckResponse_Nodes{
						{RelativePath: "repository-1", Nodes: []string{"gitaly-2", "gitaly-3"}},
						{RelativePath: "repository-2", Nodes: []string{"gitaly-1"}},
					},
				}, nil
			},
			output: `Virtual storage: test-virtual-storage
  Current read-only primary: test-current-primary
  Previous write-enabled primary: test-previous-primary
    Nodes with data loss from failing over from test-previous-primary:
      repository-1: gitaly-2, gitaly-3
      repository-2: gitaly-1
`,
		},
		{
			desc:            "multiple virtual storages",
			virtualStorages: []*config.VirtualStorage{{Name: "test-virtual-storage-2"}, {Name: "test-virtual-storage-1"}},
			datalossCheck: func(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				return &gitalypb.DatalossCheckResponse{
					PreviousWritablePrimary: "test-previous-primary",
					IsReadOnly:              true,
					CurrentPrimary:          "test-current-primary",
					OutdatedNodes: []*gitalypb.DatalossCheckResponse_Nodes{
						{RelativePath: "repository-1", Nodes: []string{"gitaly-2", "gitaly-3"}},
						{RelativePath: "repository-2", Nodes: []string{"gitaly-1"}},
					},
				}, nil
			},
			output: `Virtual storage: test-virtual-storage-1
  Current read-only primary: test-current-primary
  Previous write-enabled primary: test-previous-primary
    Nodes with data loss from failing over from test-previous-primary:
      repository-1: gitaly-2, gitaly-3
      repository-2: gitaly-1
Virtual storage: test-virtual-storage-2
  Current read-only primary: test-current-primary
  Previous write-enabled primary: test-previous-primary
    Nodes with data loss from failing over from test-previous-primary:
      repository-1: gitaly-2, gitaly-3
      repository-2: gitaly-1
`,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			mockSvc.DatalossCheckFunc = tc.datalossCheck
			cmd := newDatalossSubcommand()
			output := &bytes.Buffer{}
			cmd.output = output

			fs := cmd.FlagSet()
			require.NoError(t, fs.Parse(tc.args))
			require.Equal(t, tc.error, cmd.Exec(fs, config.Config{
				VirtualStorages: tc.virtualStorages,
				SocketPath:      ln.Addr().String(),
			}))
			require.Equal(t, tc.output, output.String())
		})
	}
}
