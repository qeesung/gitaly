package praefect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/exp/slices"
)

func TestTrackRepositoriesSubcommand(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	g1Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-1"))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-2"))
	testcfg.BuildGitalyHooks(t, g2Cfg)
	testcfg.BuildGitalySSH(t, g2Cfg)

	g1Srv := testserver.StartGitalyServer(t, g1Cfg, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Srv := testserver.StartGitalyServer(t, g2Cfg, setup.RegisterAll, testserver.WithDisablePraefect())
	defer g2Srv.Shutdown()
	defer g1Srv.Shutdown()

	g1Addr := g1Srv.Address()

	db := testdb.New(t)
	dbConf := testdb.GetConfig(t, db.Name)

	const virtualStorageName = "praefect"
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
	confPath := writeConfigToFile(t, conf)

	gitalyCC, err := client.Dial(ctx, g1Addr)
	require.NoError(t, err)
	defer testhelper.MustClose(t, gitalyCC)

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

	nodeMgr, err := nodes.NewManager(
		testhelper.SharedLogger(t),
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

	repositoryStore := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())
	assignmentStore := datastore.NewAssignmentStore(db, conf.StorageNames())

	t.Run("ok", func(t *testing.T) {
		testCases := []struct {
			desc           string
			input          string
			relativePaths  []string
			replicaPaths   []string
			args           func(inputPath string) []string
			expectedOutput string
		}{
			{
				desc:          "replica path differs from relative path",
				relativePaths: []string{"path/to/test/diff_repo1", "path/to/test/diff_repo2"},
				replicaPaths:  []string{"path/to/replica/diff_repo1", "path/to/replica/diff_repo2"},
				args: func(inputPath string) []string {
					return []string{"-replicate-immediately", "-input-path=" + inputPath}
				},
				expectedOutput: "Finished replicating repository to \"gitaly-2\".\n",
			},
			{
				desc:          "immediate replication",
				relativePaths: []string{"path/to/test/repo1", "path/to/test/repo2"},
				args: func(inputPath string) []string {
					return []string{"-replicate-immediately", "-input-path=" + inputPath}
				},
				expectedOutput: "Finished replicating repository to \"gitaly-2\".\n",
			},
			{
				desc:          "no immediate replication",
				relativePaths: []string{"path/to/test/repo3", "path/to/test/repo4"},
				args: func(inputPath string) []string {
					return []string{"-input-path=" + inputPath}
				},
				expectedOutput: "Added replication job to replicate repository to \"gitaly-2\".\n",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.desc, func(t *testing.T) {
				tempDir := testhelper.TempDir(t)
				inputPath := filepath.Join(tempDir, "input")
				input, err := os.Create(inputPath)
				require.NoError(t, err)

				if len(tc.replicaPaths) == 0 {
					tc.replicaPaths = tc.relativePaths
				}

				for i, relPath := range tc.relativePaths {
					exists, err := repositoryStore.RepositoryExists(ctx, virtualStorageName, relPath)
					require.NoError(t, err)
					require.False(t, exists)

					replicaPath := tc.replicaPaths[i]
					// create the repo on Gitaly without Praefect knowing
					require.NoError(t, createRepoThroughGitaly1(replicaPath))
					require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
					require.NoDirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))

					// Write repo details to input file
					repoEntry, err := json.Marshal(trackRepositoryRequest{
						RelativePath:         relPath,
						ReplicaPath:          replicaPath,
						VirtualStorage:       virtualStorageName,
						AuthoritativeStorage: authoritativeStorage,
					})
					require.NoError(t, err)
					_, err = fmt.Fprintf(input, string(repoEntry)+"\n")
					require.NoError(t, err)
				}
				require.NoError(t, input.Close())

				stdout, stderr, err := runApp(append([]string{"-config", confPath, trackRepositoriesCmdName}, tc.args(inputPath)...))
				assert.Empty(t, stderr)
				require.NoError(t, err)
				assert.Contains(t, stdout, tc.expectedOutput)

				replicateImmediately := slices.Contains(tc.args(inputPath), "-replicate-immediately")
				for i, relPath := range tc.relativePaths {
					repositoryID, err := repositoryStore.GetRepositoryID(ctx, virtualStorageName, relPath)
					require.NoError(t, err)

					assignments, err := assignmentStore.GetHostAssignments(ctx, virtualStorageName, repositoryID)
					require.NoError(t, err)
					require.Len(t, assignments, 2)
					assert.Contains(t, assignments, g1Cfg.Storages[0].Name)
					assert.Contains(t, assignments, g2Cfg.Storages[0].Name)

					exists, err := repositoryStore.RepositoryExists(ctx, virtualStorageName, relPath)
					require.NoError(t, err)
					assert.True(t, exists)

					if !replicateImmediately {
						queue := datastore.NewPostgresReplicationEventQueue(db)
						events, err := queue.Dequeue(ctx, virtualStorageName, g2Cfg.Storages[0].Name, 1)
						require.NoError(t, err)
						require.Len(t, events, 1)
						assert.Equal(t, relPath, events[0].Job.RelativePath)
					} else {
						replicaPath := tc.replicaPaths[i]
						require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
					}
				}
			})
		}
	})

	trackRepo := func(relativePath string) error {
		repositoryID, err := repositoryStore.ReserveRepositoryID(ctx, virtualStorageName, relativePath)
		if err != nil {
			return err
		}
		return repositoryStore.CreateRepository(
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

	outputLines := func(inputPath string, requestCt int, lines []string) []string {
		var output []string

		output = append(output, fmt.Sprintf("Validating repository information in %q", inputPath))
		output = append(output, fmt.Sprintf("Found %v invalid request(s) in %q:", requestCt, inputPath))
		output = append(output, lines...)

		return output
	}

	const invalidEntryErr = "invalid entries found, aborting"

	t.Run("fail", func(t *testing.T) {
		for _, tc := range []struct {
			desc           string
			args           func(inputPath string) []string
			input          string
			requestCt      int
			expectedOutput []string
			expectedError  string
			trackedPath    string
		}{
			{
				desc: "positional arguments",
				args: func(inputPath string) []string {
					return []string{"-input-path=" + inputPath, "positional-arg"}
				},
				expectedError: "track-repositories doesn't accept positional arguments",
			},
			{
				desc: "no required flag 'input-path'",
				args: func(string) []string {
					return nil
				},
				expectedError: `Required flag "input-path" not set`,
			},
			{
				desc:          "empty input",
				input:         "",
				expectedError: "no repository information found",
			},
			{
				desc:      "invalid JSON",
				input:     "@hashed/01/23/01234567890123456789.git",
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "", replica_path: ""`,
					"    invalid character '@' looking for beginning of value",
				},
				expectedError: invalidEntryErr,
			},
			{
				desc:      "missing relative path",
				input:     `{"replica_path":"r","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "", replica_path: "r"`,
					`    "relative-path" is a required parameter`,
				},
				expectedError: invalidEntryErr,
			},
			{
				desc:      "missing replica path",
				input:     `{"relative_path":"r","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "r", replica_path: ""`,
					`    "replica-path" is a required parameter`,
				},
				expectedError: invalidEntryErr,
			},
			{
				desc:      "invalid virtual storage",
				input:     `{"virtual_storage":"foo","relative_path":"bar","replica_path":"bar","authoritative_storage":"gitaly-1"}`,
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "bar", replica_path: "bar"`,
					`    checking repository on disk: virtual storage "foo" not found`,
				},
				expectedError: invalidEntryErr,
			},
			{
				desc:      "repo does not exist",
				input:     `{"relative_path":"not_a_repo","replica_path":"foo","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "not_a_repo", replica_path: "foo"`,
					"    not a valid git repository",
				},
				expectedError: invalidEntryErr,
			},
			{
				desc: "duplicate relative path",
				input: `{"virtual_storage":"praefect","relative_path":"duplicate","replica_path":"foo","authoritative_storage":"gitaly-1"}
					{"virtual_storage":"praefect","relative_path":"duplicate","replica_path":"foo","authoritative_storage":"gitaly-1"}`,
				requestCt: 2,
				expectedOutput: []string{
					`  line 1, relative_path: "duplicate", replica_path: "foo"`,
					"    not a valid git repository",
					`  line 2, relative_path: "duplicate", replica_path: "foo"`,
					"    duplicate entries for relative_path, line [1 2]",
				},
				expectedError: invalidEntryErr,
			},
			{
				desc:      "repo is already tracked",
				input:     `{"relative_path":"already_tracked","replica_path":"foo","virtual_storage":"praefect","authoritative_storage":"gitaly-1"}`,
				requestCt: 1,
				expectedOutput: []string{
					`  line 1, relative_path: "already_tracked", replica_path: "foo"`,
					"    repository is already tracked by Praefect",
				},
				expectedError: invalidEntryErr,
				trackedPath:   "already_tracked",
			},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				tempDir := testhelper.TempDir(t)
				inputPath := filepath.Join(tempDir, "input_file")
				f, err := os.Create(inputPath)
				require.NoError(t, err)
				_, err = f.Write([]byte(tc.input))
				require.NoError(t, err)
				require.NoError(t, f.Close())

				if tc.trackedPath != "" {
					require.NoError(t, trackRepo(tc.trackedPath))
				}

				args := []string{"-replicate-immediately", "-input-path=" + inputPath}
				if tc.args != nil {
					args = tc.args(inputPath)
				}
				stdout, stderr, err := runApp(append([]string{"-config", confPath, trackRepositoriesCmdName}, args...))
				assert.Empty(t, stderr)
				require.Error(t, err)

				if len(tc.expectedOutput) > 0 {
					require.Subset(t, strings.Split(stdout, "\n"), outputLines(inputPath, tc.requestCt, tc.expectedOutput))
				}

				require.Contains(t, err.Error(), tc.expectedError)
			})
		}
	})
}
