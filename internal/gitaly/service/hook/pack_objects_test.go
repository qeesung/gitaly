package hook

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestPackObjectsInvalidArgument(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.PackObjectsHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{}), "empty repository should result in an error")
	_, err = stream.Recv()

	testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
}

func TestPackObjectsCommandValidation(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		args  []string
		valid bool
	}{
		{[]string{"pack-objects"}, true},
		{[]string{"rm", "-rf"}, false},
	}

	for _, tc := range testCases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stream, err := client.PackObjectsHook(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
				Repository: testRepo,
				Args:       tc.args,
			}))
			resp, err := stream.Recv()
			require.NoError(t, err, "response")

			require.Equal(t, tc.valid, resp.CommandAccepted, "command accepted")
		})
	}
}

func TestPackObjectsSuccess(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		desc  string
		stdin string
		args  []string
	}{
		{
			"clone 1 branch",
			"3dd08961455abf80ef9115f4afdc1c6f968b503c\n--not\n\n",
			[]string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"},
		},
		{
			"shallow clone 1 branch",
			"--shallow 1e292f8fedd741b75372e19097c76d327140c312\n1e292f8fedd741b75372e19097c76d327140c312\n--not\n\n",
			[]string{"--shallow-file", "", "pack-objects", "--revs", "--thin", "--stdout", "--shallow", "--progress", "--delta-base-offset", "--include-tag"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.PackObjectsHook(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
				Repository: testRepo,
				Args:       tc.args,
			}))
			resp, err := stream.Recv()
			require.NoError(t, err, "response")

			require.True(t, resp.CommandAccepted, "command accepted")

			require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
				Stdin: []byte(tc.stdin),
			}), "send stdin")
			require.NoError(t, stream.CloseSend(), "close send")

			var stdout []byte
			for err == nil {
				resp, err = stream.Recv()
				stdout = append(stdout, resp.GetStdout()...)
				if stderr := resp.GetStderr(); len(stderr) > 0 {
					t.Log(string(stderr))
				}
			}
			require.Equal(t, io.EOF, err)

			testhelper.MustRunCommand(
				t,
				bytes.NewReader(stdout),
				"git", "-C", testRepoPath, "index-pack", "--stdin", "--fix-thin",
			)
		})
	}
}

func TestParsePackObjectsArgs(t *testing.T) {
	testCases := []struct {
		args []string
		*packObjectsArgs
		valid bool
	}{
		{[]string{"pack-objects"}, &packObjectsArgs{}, true},
		{[]string{"--shallow-file", "", "pack-objects"}, &packObjectsArgs{shallowFile: true}, true},
		{[]string{"pack-objects", "--foo", "-x"}, &packObjectsArgs{flags: []string{"--foo", "-x"}}, true},
		{[]string{"--shallow-file", "", "pack-objects", "--foo", "-x"}, &packObjectsArgs{shallowFile: true, flags: []string{"--foo", "-x"}}, true},
		{[]string{"zpack-objects"}, nil, false},
		{[]string{"--shallow-file", "z", "pack-objects"}, nil, false},
		{[]string{"-c", "foo=bar", "pack-objects"}, nil, false},
		{[]string{"pack-objects", "--foo", "x"}, nil, false},
		{[]string{"--shallow-file", "", "pack-objects", "--foo", "x"}, nil, false},
	}

	for _, tc := range testCases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args, valid := parsePackObjectsArgs(tc.args)
			if !tc.valid {
				require.False(t, valid, "valid")
			} else {
				require.Equal(t, *tc.packObjectsArgs, *args)
			}
		})
	}
}
