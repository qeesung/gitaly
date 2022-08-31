//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestAddRepositories_FlagSet(t *testing.T) {
	t.Parallel()
	cmd := &trackRepositories{}
	fs := cmd.FlagSet()
	require.NoError(t, fs.Parse([]string{"--input-path", "/dev/stdin", "--replicate-immediately", "true"}))
	require.Equal(t, "/dev/stdin", cmd.inputPath)
	require.Equal(t, true, cmd.replicateImmediately)
}

func TestAddRepositories_Exec_invalidInput(t *testing.T) {
	t.Parallel()
	g1Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-1"))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-2"))

	g1Srv := testserver.StartGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Srv := testserver.StartGitalyServer(t, g2Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	defer g2Srv.Shutdown()
	defer g1Srv.Shutdown()
	g1Addr := g1Srv.Address()

	db := testdb.New(t)
	dbConf := testdb.GetConfig(t, db.Name)

	virtualStorageName := "praefect"
	conf := config.Config{
		AllowLegacyElectors: true,
		SocketPath:          testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorageName,
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
					{Storage: g2Cfg.Storages[0].Name, Address: g2Srv.Address()},
				},
				DefaultReplicationFactor: 2,
			},
		},
		DB: dbConf,
		Failover: config.Failover{
			Enabled:          true,
			ElectionStrategy: config.ElectionStrategyPerRepository,
		},
	}
	rs := datastore.NewPostgresRepositoryStore(db, nil)
	ctx := testhelper.Context(t)
	inputFile := "input_file"

	trackRepo := func(relativePath string) error {
		repositoryID, err := rs.ReserveRepositoryID(ctx, virtualStorageName, relativePath)
		if err != nil {
			return err
		}
		return rs.CreateRepository(
			ctx,
			repositoryID,
			virtualStorageName,
			relativePath,
			relativePath,
			g1Cfg.Storages[0].Name,
			nil,
			nil,
			false,
			false,
		)
	}

	invalidEntryErr := "invalid entries found, aborting"

	testCases := []struct {
		input          string
		desc           string
		expectedOutput string
		expectedError  string
		trackedPath    string
	}{
		{
			input:         "",
			desc:          "empty input",
			expectedError: "no repository information found",
		},
		{
			input:          `{"foo":"bar"}`,
			desc:           "unexpected key in JSON",
			expectedOutput: `json: unknown field "foo"`,
			expectedError:  invalidEntryErr,
		},
		{
			input:          `{"virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
			desc:           "missing path",
			expectedOutput: `"repository" is a required parameter`,
			expectedError:  invalidEntryErr,
		},
		{
			input:          `{"virtual_storage":"foo","relative_path":"bar","authoritative_storage":"gitaly-1"}`,
			desc:           "invalid virtual storage",
			expectedOutput: `virtual storage "foo" not found`,
			expectedError:  invalidEntryErr,
		},
		{
			input:          `{"relative_path":"not_a_repo","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
			desc:           "repo does not exist",
			expectedOutput: "not a valid git repository",
			expectedError:  invalidEntryErr,
		},
		{
			input: `{"virtual_storage":"praefect","relative_path":"duplicate","authoritative_storage":"gitaly-1"}
{"virtual_storage":"praefect","relative_path":"duplicate","authoritative_storage":"gitaly-1"}`,
			desc:           "duplicate path",
			expectedOutput: "duplicate entries for relative_path",
			expectedError:  invalidEntryErr,
		},
		{
			input:          `{"relative_path":"already_tracked","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
			desc:           "repo is already tracked",
			expectedOutput: "repository is already tracked by Praefect",
			expectedError:  invalidEntryErr,
			trackedPath:    "already_tracked",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			nodeMgr, err := nodes.NewManager(
				testhelper.NewDiscardingLogEntry(t),
				conf,
				db.DB,
				nil,
				promtest.NewMockHistogramVec(),
				protoregistry.GitalyProtoPreregistered,
				nil,
				nil,
				nil,
			)
			require.NoError(t, err)
			nodeMgr.Start(0, time.Hour)
			defer nodeMgr.Stop()

			tempDir := testhelper.TempDir(t)
			f, err := os.Create(filepath.Join(tempDir, inputFile))
			require.NoError(t, err)
			_, err = f.Write([]byte(tc.input))
			require.NoError(t, err)
			require.NoError(t, f.Close())

			var stdout bytes.Buffer

			if tc.trackedPath != "" {
				require.NoError(t, trackRepo(tc.trackedPath))
			}

			addReposCmd := &trackRepositories{
				inputPath:            filepath.Join(tempDir, inputFile),
				replicateImmediately: true,
				logger:               logger,
				w:                    &stdout,
			}
			err = addReposCmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf)
			require.Error(t, err)

			if tc.expectedOutput != "" {
				require.Contains(t, stdout.String(), tc.expectedOutput)
			}
			require.Contains(t, err.Error(), tc.expectedError)
		})
	}
}

func TestAddRepositories_Exec(t *testing.T) {
	t.Parallel()
	g1Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-1"))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-2"))
	testcfg.BuildGitalyHooks(t, g2Cfg)
	testcfg.BuildGitalySSH(t, g2Cfg)

	g1Srv := testserver.StartGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Srv := testserver.StartGitalyServer(t, g2Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	defer g2Srv.Shutdown()
	defer g1Srv.Shutdown()

	g1Addr := g1Srv.Address()

	db := testdb.New(t)
	dbConf := testdb.GetConfig(t, db.Name)

	virtualStorageName := "praefect"
	conf := config.Config{
		AllowLegacyElectors: true,
		SocketPath:          testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorageName,
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
					{Storage: g2Cfg.Storages[0].Name, Address: g2Srv.Address()},
				},
				DefaultReplicationFactor: 2,
			},
		},
		DB: dbConf,
		Failover: config.Failover{
			Enabled:          true,
			ElectionStrategy: config.ElectionStrategyPerRepository,
		},
	}

	gitalyCC, err := client.Dial(g1Addr, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, gitalyCC.Close()) }()
	ctx := testhelper.Context(t)

	gitaly1RepositoryClient := gitalypb.NewRepositoryServiceClient(gitalyCC)

	createRepoThroughGitaly1 := func(relativePath string) error {
		_, err := gitaly1RepositoryClient.CreateRepository(
			ctx,
			&gitalypb.CreateRepositoryRequest{
				Repository: &gitalypb.Repository{
					StorageName:  g1Cfg.Storages[0].Name,
					RelativePath: relativePath,
				},
			})
		return err
	}

	authoritativeStorage := g1Cfg.Storages[0].Name
	logger := testhelper.NewDiscardingLogger(t)

	t.Run("ok", func(t *testing.T) {
		testCases := []struct {
			relativePaths        []string
			desc                 string
			replicateImmediately bool
			expectedOutput       string
		}{
			{
				relativePaths:        []string{"path/to/test/repo1", "path/to/test/repo2"},
				desc:                 "immediate replication",
				replicateImmediately: true,
				expectedOutput:       "Finished replicating repository to \"gitaly-2\".\n",
			},
			{
				relativePaths:        []string{"path/to/test/repo3", "path/to/test/repo4"},
				desc:                 "no immediate replication",
				replicateImmediately: false,
				expectedOutput:       "Added replication job to replicate repository to \"gitaly-2\".\n",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.desc, func(t *testing.T) {
				nodeMgr, err := nodes.NewManager(
					testhelper.NewDiscardingLogEntry(t),
					conf,
					db.DB,
					nil,
					promtest.NewMockHistogramVec(),
					protoregistry.GitalyProtoPreregistered,
					nil,
					nil,
					nil,
				)
				require.NoError(t, err)
				nodeMgr.Start(0, time.Hour)
				defer nodeMgr.Stop()

				tempDir := testhelper.TempDir(t)
				input, err := os.Create(filepath.Join(tempDir, "input"))
				require.NoError(t, err)

				repoDS := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
				for _, path := range tc.relativePaths {
					exists, err := repoDS.RepositoryExists(ctx, virtualStorageName, path)
					require.NoError(t, err)
					require.False(t, exists)

					// create the repo on Gitaly without Praefect knowing
					require.NoError(t, createRepoThroughGitaly1(path))
					require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, path))
					require.NoDirExists(t, filepath.Join(g2Cfg.Storages[0].Path, path))

					// Write repo details to input file
					repoEntry, err := json.Marshal(trackRepositoryRequest{RelativePath: path, VirtualStorage: virtualStorageName, AuthoritativeStorage: authoritativeStorage})
					require.NoError(t, err)
					fmt.Fprintf(input, string(repoEntry)+"\n")
				}

				var stdout bytes.Buffer

				addRepoCmd := &trackRepositories{
					inputPath:            filepath.Join(tempDir, "input"),
					replicateImmediately: tc.replicateImmediately,
					logger:               logger,
					w:                    &stdout,
				}

				require.NoError(t, addRepoCmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))
				assert.Contains(t, stdout.String(), tc.expectedOutput)

				as := datastore.NewAssignmentStore(db, conf.StorageNames())

				for _, path := range tc.relativePaths {
					repositoryID, err := repoDS.GetRepositoryID(ctx, virtualStorageName, path)
					require.NoError(t, err)

					assignments, err := as.GetHostAssignments(ctx, virtualStorageName, repositoryID)
					require.NoError(t, err)
					require.Len(t, assignments, 2)
					assert.Contains(t, assignments, g1Cfg.Storages[0].Name)
					assert.Contains(t, assignments, g2Cfg.Storages[0].Name)

					exists, err := repoDS.RepositoryExists(ctx, virtualStorageName, path)
					require.NoError(t, err)
					assert.True(t, exists)

					if !tc.replicateImmediately {
						queue := datastore.NewPostgresReplicationEventQueue(db)
						events, err := queue.Dequeue(ctx, virtualStorageName, g2Cfg.Storages[0].Name, 1)
						require.NoError(t, err)
						assert.Len(t, events, 1)
						assert.Equal(t, path, events[0].Job.RelativePath)
					} else {
						require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, path))
					}
				}
			})
		}
	})
}
