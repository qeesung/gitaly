package praefect

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service/info"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestAcceptDatalossSubcommand(t *testing.T) {
	testhelper.SkipQuarantinedTest(t, "https://gitlab.com/gitlab-org/gitaly/-/issues/5080")

	t.Parallel()
	const (
		vs   = "test-virtual-storage-1"
		repo = "test-repository-1"
		st1  = "test-physical-storage-1"
		st2  = "test-physical-storage-2"
		st3  = "test-physical-storage-3"
	)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: vs,
				Nodes: []*config.Node{
					{Address: "stub", Storage: st1},
					{Address: "stub", Storage: st2},
					{Address: "stub", Storage: st3},
				},
			},
		},
	}
	ctx := testhelper.Context(t)

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	startingGenerations := map[string]int{st1: 1, st2: 0, st3: datastore.GenerationUnknown}

	repoCreated := false
	for storage, generation := range startingGenerations {
		if generation == datastore.GenerationUnknown {
			continue
		}

		if !repoCreated {
			repoCreated = true
			require.NoError(t, rs.CreateRepository(ctx, 1, vs, repo, repo, storage, nil, nil, false, false))
		}

		require.NoError(t, rs.SetGeneration(ctx, 1, storage, repo, generation))
	}

	ln, clean := listenAndServe(t, []svcRegistrar{registerPraefectInfoServer(info.NewServer(conf, rs, nil, nil, nil))})
	defer clean()

	conf.SocketPath = ln.Addr().String()
	confPath := writeConfigToFile(t, conf)

	type errorMatcher func(t *testing.T, err error)

	matchErrorMsg := func(expected string) errorMatcher {
		return func(t *testing.T, actual error) {
			t.Helper()
			require.EqualError(t, actual, expected)
		}
	}

	for _, tc := range []struct {
		desc                string
		args                []string
		virtualStorages     []*config.VirtualStorage
		datalossCheck       func(context.Context, *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error)
		output              string
		matchError          errorMatcher
		expectedGenerations map[string]int
	}{
		{
			desc:                "missing virtual storage",
			args:                []string{"-repository", "test-repository-1", "-authoritative-storage", "test-physical-storage-2"},
			matchError:          matchErrorMsg(`Required flag "virtual-storage" not set`),
			expectedGenerations: startingGenerations,
		},
		{
			desc:                "missing repository",
			args:                []string{"-virtual-storage", "test-virtual-storage-1", "-authoritative-storage", "test-physical-storage-2"},
			matchError:          matchErrorMsg(`Required flag "repository" not set`),
			expectedGenerations: startingGenerations,
		},
		{
			desc:                "missing authoritative storage",
			args:                []string{"-virtual-storage", "test-virtual-storage-1", "-repository", "test-repository-1"},
			matchError:          matchErrorMsg(`Required flag "authoritative-storage" not set`),
			expectedGenerations: startingGenerations,
		},
		{
			desc: "positional arguments",
			args: []string{"-virtual-storage", "test-virtual-storage-1", "-repository", "test-repository-1", "-authoritative-storage", "test-physical-storage-2", "positional-arg"},
			matchError: func(t *testing.T, actual error) {
				t.Helper()
				require.Equal(t, unexpectedPositionalArgsError{Command: "accept-dataloss"}, actual)
			},
			expectedGenerations: startingGenerations,
		},
		{
			desc:                "non-existent virtual storage",
			args:                []string{"-virtual-storage", "non-existent", "-repository", "test-repository-1", "-authoritative-storage", "test-physical-storage-2"},
			matchError:          matchErrorMsg(`set authoritative storage: rpc error: code = InvalidArgument desc = unknown virtual storage: "non-existent"`),
			expectedGenerations: startingGenerations,
		},
		{
			desc:                "non-existent authoritative storage",
			args:                []string{"-virtual-storage", "test-virtual-storage-1", "-repository", "test-repository-1", "-authoritative-storage", "non-existent"},
			matchError:          matchErrorMsg(`set authoritative storage: rpc error: code = InvalidArgument desc = unknown authoritative storage: "non-existent"`),
			expectedGenerations: startingGenerations,
		},
		{
			desc:                "non-existent repository",
			args:                []string{"-virtual-storage", "test-virtual-storage-1", "-repository", "non-existent", "-authoritative-storage", "test-physical-storage-2"},
			matchError:          matchErrorMsg(`set authoritative storage: rpc error: code = InvalidArgument desc = repository "non-existent" does not exist on virtual storage "test-virtual-storage-1"`),
			expectedGenerations: startingGenerations,
		},
		{
			desc: "success",
			args: []string{"-virtual-storage", "test-virtual-storage-1", "-repository", "test-repository-1", "-authoritative-storage", "test-physical-storage-2"},
			matchError: func(t *testing.T, actual error) {
				t.Helper()
				require.NoError(t, actual)
			},
			expectedGenerations: map[string]int{st1: 1, st2: 2, st3: datastore.GenerationUnknown},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			app := cli.App{
				Commands: []*cli.Command{
					newAcceptDatalossCommand(),
				},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "config",
						Value: confPath,
					},
				},
			}

			err := app.Run(append([]string{progname, "accept-dataloss"}, tc.args...))
			tc.matchError(t, err)

			for storage, expected := range tc.expectedGenerations {
				actual, err := rs.GetGeneration(ctx, 1, storage)
				require.NoError(t, err)
				require.Equal(t, expected, actual, storage)
			}
		})
	}
}
