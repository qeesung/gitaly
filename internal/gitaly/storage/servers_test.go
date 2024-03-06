package storage_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/grpc/metadata"
)

func TestExtractGitalyServers(t *testing.T) {
	ctxOuter := testhelper.Context(t)

	testCases := []struct {
		desc     string
		metadata metadata.MD
		info     storage.GitalyServers
	}{
		{
			desc:     "no gitaly-servers metadata",
			metadata: metadata.Pairs("not-gitaly-servers", "definitely not JSON camouflaging in base64"),
		},
		{
			desc:     "metadata not encoded in base64",
			metadata: metadata.Pairs("gitaly-servers", "definitely not base64"),
		},
		{
			desc:     "encoded metadata is not JSON",
			metadata: metadata.Pairs("gitaly-servers", base64.StdEncoding.EncodeToString([]byte("definitely not JSON"))),
		},
		{
			desc:     "encoded JSON is not of the expected format",
			metadata: metadata.Pairs("gitaly-servers", base64.StdEncoding.EncodeToString([]byte(`{"default":"string"}`))),
		},
		{
			desc:     "properly-encoded string",
			metadata: metadata.Pairs("gitaly-servers", base64.StdEncoding.EncodeToString([]byte(`{"default":{"address":"unix:///tmp/sock","token":"hunter1"}}`))),
			info:     storage.GitalyServers{"default": storage.ServerInfo{Address: "unix:///tmp/sock", Token: "hunter1"}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(ctxOuter, testCase.metadata)

			info, err := storage.ExtractGitalyServers(ctx)
			if testCase.info == nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, testCase.info, info)
			}
		})
	}
}

func TestInjectGitalyServers(t *testing.T) {
	check := func(t *testing.T, ctx context.Context) {
		t.Helper()

		newCtx, err := storage.InjectGitalyServers(ctx, "gitaly-1", "1.1.1.1", "secret")
		require.NoError(t, err)

		md, found := metadata.FromOutgoingContext(newCtx)
		require.True(t, found)

		gs, found := md["gitaly-servers"]
		require.True(t, found)

		require.Len(t, gs, 1)

		var servers map[string]interface{}
		require.NoError(t, json.NewDecoder(base64.NewDecoder(base64.StdEncoding, strings.NewReader(gs[0]))).Decode(&servers), "received %s", gs[0])
		require.EqualValues(t, map[string]interface{}{"gitaly-1": map[string]interface{}{"address": "1.1.1.1", "token": "secret"}}, servers)
	}

	t.Run("brand new context", func(t *testing.T) {
		// We need to check for metadata and thus cannot use the testhelper context, which injects feature
		// flags.
		ctx := context.Background()

		check(t, ctx)
	})

	t.Run("context with existing outgoing metadata should not be re-written", func(t *testing.T) {
		existing := metadata.New(map[string]string{"foo": "bar"})

		// We need to check for metadata and thus cannot use the testhelper context, which injects feature
		// flags.
		ctx := metadata.NewOutgoingContext(context.Background(), existing)
		check(t, ctx)

		md, found := metadata.FromOutgoingContext(ctx)
		require.True(t, found)
		require.Equal(t, []string{"bar"}, md["foo"])
	})
}

func TestInjectGitalyServersEnv(t *testing.T) {
	t.Run("GITALY_SERVERS not set", func(t *testing.T) {
		testhelper.Unsetenv(t, "GITALY_SERVERS")

		ctx := testhelper.Context(t)

		newCtx, err := storage.InjectGitalyServersEnv(ctx)

		require.NoError(t, err)
		require.Equal(t, ctx, newCtx)
	})

	for _, tc := range []struct {
		desc             string
		gitalyServersEnv string
		expectedErr      string
		expectedInfo     storage.GitalyServers
	}{
		{
			desc:             "GITALY_SERVERS invalid",
			gitalyServersEnv: base64.StdEncoding.EncodeToString([]byte("definitely not JSON")),
			expectedErr:      "injecting GITALY_SERVERS: failed unmarshalling json: invalid character 'd' looking for beginning of value",
		},
		{
			desc:             "GITALY_SERVERS set",
			gitalyServersEnv: base64.StdEncoding.EncodeToString([]byte(`{"default":{"address":"unix:///tmp/sock","token":"hunter1"}}`)),
			expectedInfo:     storage.GitalyServers{"default": storage.ServerInfo{Address: "unix:///tmp/sock", Token: "hunter1"}},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GITALY_SERVERS", tc.gitalyServersEnv)

			ctx, err := storage.InjectGitalyServersEnv(testhelper.Context(t))
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
				return
			}

			info, err := storage.ExtractGitalyServers(ctx)
			require.NoError(t, err)

			require.Equal(t, tc.expectedInfo, info)
		})
	}
}
