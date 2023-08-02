package hook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	hookPkg "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/limiter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/streamcache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func runTestsWithRuntimeDir(t *testing.T, testFunc func(*testing.T, string)) {
	t.Helper()

	t.Run("no runtime dir", func(t *testing.T) {
		testFunc(t, "")
	})

	t.Run("with runtime dir", func(t *testing.T) {
		testFunc(t, testhelper.TempDir(t))
	})
}

func cfgWithCache(t *testing.T, minOccurrences int) config.Cfg {
	cfg := testcfg.Build(t)
	cfg.PackObjectsCache.Enabled = true
	cfg.PackObjectsCache.Dir = testhelper.TempDir(t)
	cfg.PackObjectsCache.MinOccurrences = minOccurrences
	return cfg
}

func TestParsePackObjectsArgs(t *testing.T) {
	testCases := []struct {
		desc string
		args []string
		out  *packObjectsArgs
		err  error
	}{
		{desc: "no args", args: []string{"pack-objects", "--stdout"}, out: &packObjectsArgs{}},
		{desc: "no args shallow", args: []string{"--shallow-file", "", "pack-objects", "--stdout"}, out: &packObjectsArgs{shallowFile: true}},
		{desc: "with args", args: []string{"pack-objects", "--foo", "-x", "--stdout"}, out: &packObjectsArgs{flags: []string{"--foo", "-x"}}},
		{desc: "with args shallow", args: []string{"--shallow-file", "", "pack-objects", "--foo", "--stdout", "-x"}, out: &packObjectsArgs{shallowFile: true, flags: []string{"--foo", "-x"}}},
		{desc: "missing stdout", args: []string{"pack-objects"}, err: errNoStdout},
		{desc: "no pack objects", args: []string{"zpack-objects"}, err: errNoPackObjects},
		{desc: "non empty shallow", args: []string{"--shallow-file", "z", "pack-objects"}, err: errNoPackObjects},
		{desc: "bad global", args: []string{"-c", "foo=bar", "pack-objects"}, err: errNoPackObjects},
		{desc: "non flag arg", args: []string{"pack-objects", "--foo", "x"}, err: errNonFlagArg},
		{desc: "non flag arg shallow", args: []string{"--shallow-file", "", "pack-objects", "--foo", "x"}, err: errNonFlagArg},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			args, err := parsePackObjectsArgs(tc.args)
			require.Equal(t, tc.out, args)
			require.Equal(t, tc.err, err)
		})
	}
}

func TestServer_PackObjectsHook_separateContext(t *testing.T) {
	testhelper.SkipQuarantinedTest(t, "https://gitlab.com/gitlab-org/gitaly/-/issues/5548")

	t.Parallel()
	runTestsWithRuntimeDir(t, testServerPackObjectsHookSeparateContextWithRuntimeDir)
}

func testServerPackObjectsHookSeparateContextWithRuntimeDir(t *testing.T, runtimeDir string) {
	ctx := testhelper.Context(t)

	cfg := cfgWithCache(t, 0)
	cfg.SocketPath = runHooksServer(t, cfg, nil)

	ctx1, cancel := context.WithCancel(ctx)
	defer cancel()
	repo, repoPath := gittest.CreateRepository(t, ctx1, cfg)

	// We write a commit with a large blob such that the response needs to be split over multiple messages.
	// Otherwise it may happen that the request will finish before we can actually cancel the context.
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	req := &gitalypb.PackObjectsHookWithSidechannelRequest{
		Repository: repo,
		Args:       []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"},
	}
	stdin := commitID.String() + "\n--not\n\n"

	start1 := make(chan struct{})
	start2 := make(chan struct{})
	wg := &sync.WaitGroup{}
	wg.Add(2)

	// Call 1: sends a valid request but hangs up without reading response.
	// This should not break call 2.
	client1, conn1 := newHooksClient(t, cfg.SocketPath)
	defer conn1.Close()

	ctx1, wt1, err := hookPkg.SetupSidechannel(
		ctx1,
		git.HooksPayload{
			RuntimeDir: runtimeDir,
		},
		func(c *net.UnixConn) error {
			defer close(start2)
			<-start1
			if _, err := io.WriteString(c, stdin); err != nil {
				return err
			}
			if err := c.CloseWrite(); err != nil {
				return err
			}

			// Read one byte of the response to ensure that this call got handled
			// before the next one.
			buf := make([]byte, 1)
			_, err := io.ReadFull(c, buf)
			return err
		},
	)
	require.NoError(t, err)
	defer testhelper.MustClose(t, wt1)

	go func() {
		defer wg.Done()
		_, err := client1.PackObjectsHookWithSidechannel(ctx1, req)

		if runtime.GOOS == "darwin" {
			assert.Contains(t, []codes.Code{codes.Canceled, codes.Internal}, status.Code(err))

			if status.Code(err) == codes.Internal {
				assert.Contains(t, err.Error(), "write: socket is not connected")
			}
		} else {
			testhelper.AssertGrpcCode(t, err, codes.Canceled)
		}

		assert.NoError(t, wt1.Wait())
	}()

	// Call 2: this is a normal call with the same request as call 1
	client2, conn2 := newHooksClient(t, cfg.SocketPath)
	defer conn2.Close()

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	var stdout2 []byte
	ctx2, wt2, err := hookPkg.SetupSidechannel(
		ctx2,
		git.HooksPayload{
			RuntimeDir: runtimeDir,
		},
		func(c *net.UnixConn) error {
			<-start2
			if _, err := io.WriteString(c, stdin); err != nil {
				return err
			}
			if err := c.CloseWrite(); err != nil {
				return err
			}

			return pktline.EachSidebandPacket(c, func(band byte, data []byte) error {
				if band == 1 {
					stdout2 = append(stdout2, data...)
				}
				return nil
			})
		},
	)
	require.NoError(t, err)
	defer testhelper.MustClose(t, wt2)

	go func() {
		defer wg.Done()
		_, err := client2.PackObjectsHookWithSidechannel(ctx2, req)
		assert.NoError(t, err)
		assert.NoError(t, wt2.Wait())
	}()

	close(start1)
	wg.Wait()

	// Sanity check: second call received valid response
	gittest.ExecOpts(
		t,
		cfg,
		gittest.ExecConfig{Stdin: bytes.NewReader(stdout2)},
		"-C", repoPath, "index-pack", "--stdin", "--fix-thin",
	)
}

func TestServer_PackObjectsHook_usesCache(t *testing.T) {
	t.Parallel()
	runTestsWithRuntimeDir(t, testServerPackObjectsHookUsesCache)
}

func testServerPackObjectsHookUsesCache(t *testing.T, runtimeDir string) {
	ctx := testhelper.Context(t)

	testCases := []struct {
		name         string
		makeRequests func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest
		// shouldUseCacheOf[i] contains the position of request that generates the cache for
		// i-th request. shouldUseCacheOf[i] == -1 means i-th request does not use cache, thus
		// create packfile
		shouldUseCacheOf []int
		minOccurrences   int
	}{
		{
			name: "all requests are identical",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
				}
			},
			shouldUseCacheOf: []int{-1, 0, 0, 0, 0},
		},
		{
			name: "requests with different protocols",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args, GitProtocol: "ssh"},
					{Repository: repository, Args: args, GitProtocol: "ssh"},
					{Repository: repository, Args: args, GitProtocol: "http"},
					{Repository: repository, Args: args, GitProtocol: "http"},
					{Repository: repository, Args: args, GitProtocol: "ssh"},
				}
			},
			shouldUseCacheOf: []int{-1, 0, -1, 2, 0},
		},
		{
			name: "requests with slightly different args",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args1 := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress"}
				args2 := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args1},
					{Repository: repository, Args: args1},
					{Repository: repository, Args: args1},
					{Repository: repository, Args: args2},
					{Repository: repository, Args: args2},
				}
			},
			shouldUseCacheOf: []int{-1, 0, 0, -1, 4},
		},
		{
			name: "requests from different remote IPs",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args, RemoteIp: "1.2.3.4"},
					{Repository: repository, Args: args, RemoteIp: "1.2.3.5"},
					{Repository: repository, Args: args, RemoteIp: "1.2.3.4"},
					{Repository: repository, Args: args, RemoteIp: "1.2.3.4"},
					{Repository: repository, Args: args, RemoteIp: "1.2.3.5"},
				}
			},
			// All from cached
			shouldUseCacheOf: []int{-1, 0, 0, 0, 0},
		},
		{
			name: "requests from different user IDs",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args, GlId: "1"},
					{Repository: repository, Args: args, GlId: "1"},
					{Repository: repository, Args: args, GlId: "1"},
					{Repository: repository, Args: args, GlId: "2"},
					{Repository: repository, Args: args, GlId: "3"},
				}
			},
			// All from cached
			shouldUseCacheOf: []int{-1, 0, 0, 0, 0},
		},
		{
			name: "requests from different usernames",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args, GlId: "user-1"},
					{Repository: repository, Args: args, GlId: "user-1"},
					{Repository: repository, Args: args, GlId: "user-1"},
					{Repository: repository, Args: args, GlId: "user-2"},
					{Repository: repository, Args: args, GlId: "user-3"},
				}
			},
			// All from cached
			shouldUseCacheOf: []int{-1, 0, 0, 0, 0},
		},
		{
			name: "min_occurrences setting is set to 1",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
				}
			},
			// The second one starts to serve cache
			shouldUseCacheOf: []int{-1, -1, 0, 0, 0},
			minOccurrences:   1,
		},
		{
			name: "min_occurrences setting is set to 5",
			makeRequests: func(repository *gitalypb.Repository) []*gitalypb.PackObjectsHookWithSidechannelRequest {
				args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}
				return []*gitalypb.PackObjectsHookWithSidechannelRequest{
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
					{Repository: repository, Args: args},
				}
			},
			shouldUseCacheOf: []int{-1, -1, -1, -1, -1},
			minOccurrences:   5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cfgWithCache(t, tc.minOccurrences)

			tlc := &streamcache.TestLoggingCache{}
			cfg.SocketPath = runHooksServer(t, cfg, []serverOption{func(s *server) {
				tlc.Cache = s.packObjectsCache
				s.packObjectsCache = tlc
			}})

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
			commitID := gittest.WriteCommit(t, cfg, repoPath)

			doRequest := func(request *gitalypb.PackObjectsHookWithSidechannelRequest) {
				var stdout []byte
				ctx, wt, err := hookPkg.SetupSidechannel(
					ctx,
					git.HooksPayload{
						RuntimeDir: runtimeDir,
					},
					func(c *net.UnixConn) error {
						if _, err := io.WriteString(c, commitID.String()+"\n--not\n\n"); err != nil {
							return err
						}
						if err := c.CloseWrite(); err != nil {
							return err
						}

						return pktline.EachSidebandPacket(c, func(band byte, data []byte) error {
							if band == 1 {
								stdout = append(stdout, data...)
							}
							return nil
						})
					},
				)
				require.NoError(t, err)
				defer testhelper.MustClose(t, wt)

				client, conn := newHooksClient(t, cfg.SocketPath)
				defer conn.Close()

				_, err = client.PackObjectsHookWithSidechannel(ctx, request)
				require.NoError(t, err)
				require.NoError(t, wt.Wait())

				gittest.ExecOpts(
					t,
					cfg,
					gittest.ExecConfig{Stdin: bytes.NewReader(stdout)},
					"-C", repoPath, "index-pack", "--stdin", "--fix-thin",
				)
			}

			requests := tc.makeRequests(repo)
			for _, request := range requests {
				doRequest(request)
			}

			entries := tlc.Entries()
			require.Len(t, entries, len(requests))

			for i := 0; i < len(requests); i++ {
				require.NoError(t, entries[i].Err)

				if tc.shouldUseCacheOf[i] == -1 {
					require.True(t, entries[i].Created, "request %d should create packfile", i)
				} else {
					require.False(t, entries[i].Created, "request %d should not create packfile", i)
					entryOfCache := entries[tc.shouldUseCacheOf[i]]
					require.Equal(t, entryOfCache.Key, entries[i].Key, "request %d does not cache key from request %d", i, tc.shouldUseCacheOf[i])
				}
			}
		})
	}
}

func TestServer_PackObjectsHookWithSidechannel(t *testing.T) {
	t.Parallel()
	runTestsWithRuntimeDir(t, testServerPackObjectsHookWithSidechannelWithRuntimeDir)
}

func testServerPackObjectsHookWithSidechannelWithRuntimeDir(t *testing.T, runtimeDir string) {
	t.Parallel()

	ctx := testhelper.Context(t)

	type setupData struct {
		repo     *gitalypb.Repository
		repoPath string
		args     []string
		stdin    string
	}

	for _, tc := range []struct {
		desc  string
		setup func(*testing.T, context.Context, config.Cfg) setupData
	}{
		{
			desc: "clone 1 branch",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
				oid := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

				return setupData{
					repo:     repo,
					repoPath: repoPath,
					stdin:    oid.String() + "\n--not\n\n",
					args:     []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"},
				}
			},
		},
		{
			desc: "shallow clone 1 branch",
			setup: func(t *testing.T, ctx context.Context, cfg config.Cfg) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstCommit := gittest.WriteCommit(t, cfg, repoPath)
				secondCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommit), gittest.WithBranch("branch"))

				return setupData{
					repo:     repo,
					repoPath: repoPath,
					stdin:    fmt.Sprintf("--shallow %[1]s\n%[1]s\n--not\n\n", secondCommit),
					args:     []string{"--shallow-file", "", "pack-objects", "--revs", "--thin", "--stdout", "--shallow", "--progress", "--delta-base-offset", "--include-tag"},
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := cfgWithCache(t, 0)

			logger, hook := test.NewNullLogger()

			cfg.SocketPath = runHooksServer(
				t,
				cfg,
				nil,
				testserver.WithLogger(logger),
			)

			setup := tc.setup(t, ctx, cfg)

			var packets []string
			ctx, wt, err := hookPkg.SetupSidechannel(
				ctx,
				git.HooksPayload{
					RuntimeDir: runtimeDir,
				},
				func(c *net.UnixConn) error {
					if _, err := io.WriteString(c, setup.stdin); err != nil {
						return err
					}
					if err := c.CloseWrite(); err != nil {
						return err
					}

					scanner := pktline.NewScanner(c)
					for scanner.Scan() {
						packets = append(packets, scanner.Text())
					}
					return scanner.Err()
				},
			)
			require.NoError(t, err)
			defer testhelper.MustClose(t, wt)

			client, conn := newHooksClient(t, cfg.SocketPath)
			defer conn.Close()

			_, err = client.PackObjectsHookWithSidechannel(ctx, &gitalypb.PackObjectsHookWithSidechannelRequest{
				Repository: setup.repo,
				Args:       setup.args,
			})
			require.NoError(t, err)

			require.NoError(t, wt.Wait())
			require.NotEmpty(t, packets)

			var packdata []byte
			for _, pkt := range packets {
				require.Greater(t, len(pkt), 4)

				switch band := pkt[4]; band {
				case 1:
					packdata = append(packdata, pkt[5:]...)
				case 2:
				default:
					t.Fatalf("unexpected band: %d", band)
				}
			}

			gittest.ExecOpts(
				t,
				cfg,
				gittest.ExecConfig{Stdin: bytes.NewReader(packdata)},
				"-C", setup.repoPath, "index-pack", "--stdin", "--fix-thin",
			)

			entry := hook.LastEntry()
			require.NotNil(t, entry)

			fields := entry.Data
			require.Equal(t, fields["pack_objects_cache.hit"], "false")
			require.Contains(t, fields, "pack_objects_cache.key")
			require.Greater(t, fields["pack_objects_cache.served_bytes"], 0)
			require.Greater(t, fields["pack_objects_cache.generated_bytes"], 0)

			total := fields["pack_objects.compression_statistics"].(string)
			require.True(t, strings.HasPrefix(total, "Total "))
			require.False(t, strings.Contains(total, "\n"))
		})
	}
}

func TestServer_PackObjectsHookWithSidechannel_invalidArgument(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runHooksServer(t, cfg, nil)
	ctx := testhelper.Context(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := []struct {
		desc        string
		req         *gitalypb.PackObjectsHookWithSidechannelRequest
		expectedErr error
	}{
		{
			desc:        "empty",
			req:         &gitalypb.PackObjectsHookWithSidechannelRequest{},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:        "repo, no args",
			req:         &gitalypb.PackObjectsHookWithSidechannelRequest{Repository: repo},
			expectedErr: status.Error(codes.InvalidArgument, "invalid pack-objects command: []: missing pack-objects"),
		},
		{
			desc:        "repo, bad args",
			req:         &gitalypb.PackObjectsHookWithSidechannelRequest{Repository: repo, Args: []string{"rm", "-rf"}},
			expectedErr: status.Error(codes.InvalidArgument, "invalid pack-objects command: [rm -rf]: missing pack-objects"),
		},
		{
			desc:        "no side-channel address",
			req:         &gitalypb.PackObjectsHookWithSidechannelRequest{Repository: repo, Args: []string{"pack-objects", "--revs", "--stdout"}},
			expectedErr: status.Error(codes.InvalidArgument, `invalid side channel address: ""`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			client, conn := newHooksClient(t, cfg.SocketPath)
			defer conn.Close()

			_, err := client.PackObjectsHookWithSidechannel(ctx, tc.req)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestServer_PackObjectsHookWithSidechannel_Canceled(t *testing.T) {
	t.Parallel()
	runTestsWithRuntimeDir(t, testServerPackObjectsHookWithSidechannelCanceledWithRuntimeDir)
}

func testServerPackObjectsHookWithSidechannelCanceledWithRuntimeDir(t *testing.T, runtimeDir string) {
	ctx := testhelper.Context(t)
	cfg := cfgWithCache(t, 0)
	cfg.SocketPath = runHooksServer(t, cfg, nil)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	ctx, wt, err := hookPkg.SetupSidechannel(
		ctx,
		git.HooksPayload{
			RuntimeDir: runtimeDir,
		},
		func(c *net.UnixConn) error {
			// Simulate a client that successfully initiates a request, but hangs up
			// before fully consuming the response.
			_, err := io.WriteString(c, fmt.Sprintf("%s\n--not\n\n", commitID))
			return err
		},
	)
	require.NoError(t, err)
	defer testhelper.MustClose(t, wt)

	client, conn := newHooksClient(t, cfg.SocketPath)
	defer conn.Close()

	_, err = client.PackObjectsHookWithSidechannel(ctx, &gitalypb.PackObjectsHookWithSidechannelRequest{
		Repository: repo,
		Args:       []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"},
	})
	testhelper.RequireGrpcCode(t, err, codes.Canceled)

	require.NoError(t, wt.Wait())
}

func withRunPackObjectsFn(
	f func(
		context.Context,
		git.CommandFactory,
		io.Writer,
		*gitalypb.PackObjectsHookWithSidechannelRequest,
		*packObjectsArgs,
		io.Reader,
		string,
	) error,
) serverOption {
	return func(s *server) {
		s.runPackObjectsFn = f
	}
}

func setupSidechannel(t *testing.T, ctx context.Context, oid string) (context.Context, *hookPkg.SidechannelWaiter, error) {
	return hookPkg.SetupSidechannel(
		ctx,
		git.HooksPayload{
			RuntimeDir: testhelper.TempDir(t),
		},
		func(c *net.UnixConn) error {
			// Simulate a client that successfully initiates a request, but hangs up
			// before fully consuming the response.
			_, err := io.WriteString(c, fmt.Sprintf("%s\n--not\n\n", oid))
			return err
		},
	)
}

func TestPackObjects_concurrencyLimit(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}

	for _, tc := range []struct {
		desc        string
		setup       func(*testing.T, config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest
		shouldLimit bool
	}{
		{
			desc: "never reach limit",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repoA, _ := gittest.CreateRepository(t, ctx, cfg)
				repoB, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "1.2.3.4",
						Repository: repoA,
						Args:       args,
					},
					{
						GlId:       "user-456",
						RemoteIp:   "1.2.3.5",
						Repository: repoB,
						Args:       args,
					},
				}
			},
			shouldLimit: false,
		},
		{
			desc: "normal IP address",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "1.2.3.4",
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   "1.2.3.4",
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: true,
		},
		{
			desc: "IP addresses including source port",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "1.2.3.4:47293",
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   "1.2.3.4:51010",
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: true,
		},
		{
			desc: "IPv4 loopback addresses",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "127.0.0.1",
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   "127.0.0.1",
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: false,
		},
		{
			desc: "IPv6 loopback addresses",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   net.IPv6loopback.String(),
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   net.IPv6loopback.String(),
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: false,
		},
		{
			desc: "invalid IP addresses",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "hello-world",
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   "hello-world",
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: false,
		},
		{
			desc: "empty IP addresses",
			setup: func(t *testing.T, cfg config.Cfg) [2]*gitalypb.PackObjectsHookWithSidechannelRequest {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return [2]*gitalypb.PackObjectsHookWithSidechannelRequest{
					{
						GlId:       "user-123",
						RemoteIp:   "",
						Repository: repo,
						Args:       args,
					},
					{
						GlId:       "user-123",
						RemoteIp:   "",
						Repository: repo,
						Args:       args,
					},
				}
			},
			shouldLimit: false,
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := cfgWithCache(t, 0)

			ticker := helper.NewManualTicker()
			monitor := limiter.NewPackObjectsConcurrencyMonitor(
				cfg.Prometheus.GRPCLatencyBuckets,
			)
			limiter := limiter.NewConcurrencyLimiter(
				limiter.NewAdaptiveLimit("staticLimit", limiter.AdaptiveSetting{Initial: 1}),
				0,
				func() helper.Ticker { return ticker },
				monitor,
			)

			registry := prometheus.NewRegistry()
			registry.MustRegister(monitor)

			receivedCh, blockCh := make(chan struct{}), make(chan struct{})
			cfg.SocketPath = runHooksServer(t, cfg, []serverOption{
				withRunPackObjectsFn(func(
					context.Context,
					git.CommandFactory,
					io.Writer,
					*gitalypb.PackObjectsHookWithSidechannelRequest,
					*packObjectsArgs,
					io.Reader,
					string,
				) error {
					receivedCh <- struct{}{}
					<-blockCh
					return nil
				}),
			},
				testserver.WithPackObjectsLimiter(limiter),
			)

			requests := tc.setup(t, cfg)

			ctx1, wt1, err := setupSidechannel(t, ctx, "1dd08961455abf80ef9115f4afdc1c6f968b503c")
			require.NoError(t, err)

			ctx2, wt2, err := setupSidechannel(t, ctx, "2dd08961455abf80ef9115f4afdc1c6f968b503")
			require.NoError(t, err)

			client, conn := newHooksClient(t, cfg.SocketPath)
			defer testhelper.MustClose(t, conn)

			var wg sync.WaitGroup
			wg.Add(2)

			errChan := make(chan error)

			// We fire off two requests. Since the concurrency limit is set at 1,
			// the first request will make it through the concurrency limiter and
			// get blocked in the function provide to withRunPackObjectsFn() above.
			// The second request will get concurrency limited and will be waiting
			// in the queue.
			// When we call Tick() on the max queue wait ticker, the second request
			// will return with an error.

			type call struct {
				ctx context.Context
				req *gitalypb.PackObjectsHookWithSidechannelRequest
			}

			for _, c := range []call{
				{ctx: ctx1, req: requests[0]},
				{ctx: ctx2, req: requests[1]},
			} {
				go func(c call) {
					defer wg.Done()
					_, err := client.PackObjectsHookWithSidechannel(c.ctx, c.req)
					if err != nil {
						errChan <- err
					}
				}(c)
			}

			if tc.shouldLimit {
				<-receivedCh

				require.NoError(t,
					testutil.GatherAndCompare(registry,
						bytes.NewBufferString(`# HELP gitaly_pack_objects_in_progress Gauge of number of concurrent in-progress calls
# TYPE gitaly_pack_objects_in_progress gauge
gitaly_pack_objects_in_progress 1
`), "gitaly_pack_objects_in_progress"))

				ticker.Tick()

				err := <-errChan
				testhelper.RequireGrpcCode(
					t,
					err,
					codes.ResourceExhausted,
				)

				close(blockCh)

				expectedMetrics := bytes.NewBufferString(`# HELP gitaly_pack_objects_dropped_total Number of requests dropped from the queue
# TYPE gitaly_pack_objects_dropped_total counter
gitaly_pack_objects_dropped_total{reason="max_time"} 1
# HELP gitaly_pack_objects_queued Gauge of number of queued calls
# TYPE gitaly_pack_objects_queued gauge
gitaly_pack_objects_queued 0
`)

				require.NoError(t,
					testutil.GatherAndCompare(registry, expectedMetrics,
						"gitaly_pack_objects_dropped_total",
						"gitaly_pack_objects_queued",
					))

				acquiringSecondsCount, err := testutil.GatherAndCount(registry,
					"gitaly_pack_objects_acquiring_seconds")
				require.NoError(t, err)

				require.Equal(t, 1, acquiringSecondsCount)
			} else {
				close(blockCh)
				<-receivedCh
				<-receivedCh
			}

			wg.Wait()
			require.NoError(t, wt1.Wait())
			require.NoError(t, wt2.Wait())
		})
	}
}
