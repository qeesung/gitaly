package main

import (
	"bytes"
	"context"
	"flag"
	"testing"
	"time"

	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestTimeFlag(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected time.Time
	}{
		{
			input:    "2020-02-03T14:15:16Z",
			expected: time.Date(2020, 2, 3, 14, 15, 16, 0, time.UTC),
		},
		{
			input:    "2020-02-03T14:15:16+02:00",
			expected: time.Date(2020, 2, 3, 14, 15, 16, 0, time.FixedZone("UTC+2", 2*60*60)),
		},
		{
			input: "",
		},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var actual time.Time
			fs := flag.NewFlagSet("dataloss", flag.ContinueOnError)
			fs.Var((*timeFlag)(&actual), "time", "")

			err := fs.Parse([]string{"-time", tc.input})
			if !tc.expected.IsZero() {
				require.NoError(t, err)
			}

			require.True(t, tc.expected.Equal(actual))
		})
	}
}

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
		desc          string
		args          []string
		datalossCheck func(context.Context, *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error)
		output        string
		error         error
	}{
		{
			desc:  "from equals to",
			args:  []string{"-from=2020-01-02T00:00:00Z", "-to=2020-01-02T00:00:00Z"},
			error: errFromNotBeforeTo,
		},
		{
			desc:  "from after to",
			args:  []string{"-from=2020-01-02T00:00:00Z", "-to=2020-01-01T00:00:00Z"},
			error: errFromNotBeforeTo,
		},
		{
			desc: "no dead jobs",
			args: []string{"-from=2020-01-02T00:00:00Z", "-to=2020-01-03T00:00:00Z"},
			datalossCheck: func(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				require.Equal(t, req.GetFrom(), &timestamp.Timestamp{Seconds: 1577923200})
				require.Equal(t, req.GetTo(), &timestamp.Timestamp{Seconds: 1578009600})
				return &gitalypb.DatalossCheckResponse{}, nil
			},
			output: "Failed replication jobs between [2020-01-02 00:00:00 +0000 UTC, 2020-01-03 00:00:00 +0000 UTC):\n",
		},
		{
			desc: "success",
			args: []string{"-from=2020-01-02T00:00:00Z", "-to=2020-01-03T00:00:00Z"},
			datalossCheck: func(_ context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
				require.Equal(t, req.GetFrom(), &timestamp.Timestamp{Seconds: 1577923200})
				require.Equal(t, req.GetTo(), &timestamp.Timestamp{Seconds: 1578009600})
				return &gitalypb.DatalossCheckResponse{ByRelativePath: map[string]int64{
					"test-repo/relative-path/2": 4,
					"test-repo/relative-path/1": 1,
					"test-repo/relative-path/3": 2,
				}}, nil
			},
			output: `Failed replication jobs between [2020-01-02 00:00:00 +0000 UTC, 2020-01-03 00:00:00 +0000 UTC):
test-repo/relative-path/1: 1 jobs
test-repo/relative-path/2: 4 jobs
test-repo/relative-path/3: 2 jobs
`,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			mockSvc.DatalossCheckFunc = tc.datalossCheck
			cmd := newDatalossSubcommand()
			output := &bytes.Buffer{}
			cmd.output = output
			require.NoError(t, cmd.FlagSet().Parse(tc.args))
			require.Equal(t, tc.error, cmd.Exec(cmd.FlagSet(), config.Config{SocketPath: ln.Addr().String()}))
			require.Equal(t, tc.output, output.String())
		})
	}
}
