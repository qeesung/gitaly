package repository

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestDeleteConfig(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testcases := []struct {
		desc    string
		addKeys []string
		reqKeys []string
	}{
		{
			desc: "empty request",
		},
		{
			desc:    "keys that don't exist",
			reqKeys: []string{"test.foo", "test.bar"},
		},
		{
			desc:    "mix of keys that do and do not exist",
			addKeys: []string{"test.bar"},
			reqKeys: []string{"test.foo", "test.bar", "test.baz"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			for _, k := range tc.addKeys {
				testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "config", k, "blabla")
			}

			_, err := client.DeleteConfig(ctx, &pb.DeleteConfigRequest{Repository: testRepo, Keys: tc.reqKeys})
			require.NoError(t, err)

			actualConfig := testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "config", "-l")
			scanner := bufio.NewScanner(bytes.NewReader(actualConfig))
			for scanner.Scan() {
				for _, k := range tc.reqKeys {
					require.False(t, strings.HasPrefix(scanner.Text(), k+"="), "key %q must not occur in config", k)
				}
			}

			require.NoError(t, scanner.Err())
		})
	}
}

func TestSetConfig(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	testcases := []struct {
		desc     string
		entries  []*pb.SetConfigRequest_Entry
		expected []string
	}{
		{
			desc: "empty request",
		},
		{
			desc: "mix of different types",
			entries: []*pb.SetConfigRequest_Entry{
				&pb.SetConfigRequest_Entry{Key: "test.foo1", Value: &pb.SetConfigRequest_Entry_ValueStr{"hello world"}},
				&pb.SetConfigRequest_Entry{Key: "test.foo2", Value: &pb.SetConfigRequest_Entry_ValueInt32{1234}},
				&pb.SetConfigRequest_Entry{Key: "test.foo3", Value: &pb.SetConfigRequest_Entry_ValueBool{true}},
			},
			expected: []string{
				"test.foo1=hello world",
				"test.foo2=1234",
				"test.foo3=true",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
			defer cleanupFn()

			_, err := client.SetConfig(ctx, &pb.SetConfigRequest{Repository: testRepo, Entries: tc.entries})
			require.NoError(t, err)

			actualConfigBytes := testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "config", "--local", "-l")
			scanner := bufio.NewScanner(bytes.NewReader(actualConfigBytes))

			var actualConfig []string
			for scanner.Scan() {
				actualConfig = append(actualConfig, scanner.Text())
			}
			require.NoError(t, scanner.Err())

			for _, entry := range tc.expected {
				require.Contains(t, actualConfig, entry)
			}
		})
	}
}
