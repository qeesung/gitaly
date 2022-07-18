//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestListUntrackedRepositories_FlagSet(t *testing.T) {
	t.Parallel()
	cmd := &listUntrackedRepositories{}
	for _, tc := range []struct {
		desc string
		args []string
		exp  []interface{}
	}{
		{
			desc: "custom values",
			args: []string{"--delimiter", ",", "--older-than", "1s"},
			exp:  []interface{}{",", time.Second},
		},
		{
			desc: "default values",
			args: nil,
			exp:  []interface{}{"\n", 6 * time.Hour},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			fs := cmd.FlagSet()
			require.NoError(t, fs.Parse(tc.args))
			require.ElementsMatch(t, tc.exp, []interface{}{cmd.delimiter, cmd.onlyIncludeOlderThan})
		})
	}
}

func TestListUntrackedRepositories_Exec(t *testing.T) {
	t.Parallel()
	g1Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-1"))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-2"))

	g1Addr := testserver.RunGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Addr := testserver.RunGitalyServer(t, g2Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	db := testdb.New(t)
	var database string
	require.NoError(t, db.QueryRow(`SELECT current_database()`).Scan(&database))
	dbConf := testdb.GetConfig(t, database)

	conf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
					{Storage: g2Cfg.Storages[0].Name, Address: g2Addr},
				},
			},
		},
		DB: dbConf,
	}

	praefectServer := testserver.StartPraefect(t, conf)

	cc, err := client.Dial(praefectServer.Address(), nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, cc.Close()) }()
	repoClient := gitalypb.NewRepositoryServiceClient(cc)
	ctx := testhelper.Context(t)

	praefectStorage := conf.VirtualStorages[0].Name

	// Repository managed by praefect, exists on gitaly-1 and gitaly-2.
	createRepo(t, ctx, repoClient, praefectStorage, "path/to/test/repo")
	out := &bytes.Buffer{}
	cmd := newListUntrackedRepositories(testhelper.NewDiscardingLogger(t), out)
	fs := cmd.FlagSet()
	require.NoError(t, fs.Parse([]string{"-older-than", "4h"}))

	// Repositories not managed by praefect.
	repo1, repo1Path := gittest.InitRepo(t, g1Cfg, g1Cfg.Storages[0])
	repo2, repo2Path := gittest.InitRepo(t, g1Cfg, g1Cfg.Storages[0])
	_, _ = gittest.InitRepo(t, g2Cfg, g2Cfg.Storages[0])

	require.NoError(t, os.Chtimes(
		repo1Path,
		time.Now().Add(-(4*time.Hour+1*time.Second)),
		time.Now().Add(-(4*time.Hour+1*time.Second))))
	require.NoError(t, os.Chtimes(
		repo2Path,
		time.Now().Add(-(4*time.Hour+1*time.Second)),
		time.Now().Add(-(4*time.Hour+1*time.Second))))

	require.NoError(t, cmd.Exec(fs, conf))

	exp := []string{
		"The following repositories were found on disk, but missing from the tracking database:",
		fmt.Sprintf(`{"relative_path":%q,"storage":"gitaly-1","virtual_storage":"praefect"}`, repo1.RelativePath),
		fmt.Sprintf(`{"relative_path":%q,"storage":"gitaly-1","virtual_storage":"praefect"}`, repo2.RelativePath),
		"", // an empty extra element required as each line ends with "delimiter" and strings.Split returns all parts
	}
	require.ElementsMatch(t, exp, strings.Split(out.String(), "\n"))
}

func createRepo(t *testing.T, ctx context.Context, repoClient gitalypb.RepositoryServiceClient, storageName, relativePath string) *gitalypb.Repository {
	t.Helper()
	repo := &gitalypb.Repository{
		StorageName:  storageName,
		RelativePath: relativePath,
	}

	_, err := repoClient.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{Repository: repo})
	require.NoError(t, err)

	return repo
}
