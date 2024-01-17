package praefect

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/service/info"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSetReplicationFactorSubcommand(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)

	for _, tc := range []struct {
		desc   string
		args   []string
		store  praefect.AssignmentStore
		error  error
		stdout string
	}{
		{
			desc:  "unexpected positional arguments",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path", "-replication-factor=1", "positional-arg"},
			error: cli.Exit(unexpectedPositionalArgsError{Command: "set-replication-factor"}, 1),
		},
		{
			desc:  "missing virtual-storage",
			args:  []string{"-relative-path=relative-path", "-replication-factor=1"},
			error: errors.New(`Required flag "virtual-storage" not set`),
		},
		{
			desc:  "missing relative-path",
			args:  []string{"-virtual-storage=virtual-storage", "-replication-factor=1"},
			error: errors.New(`Required flag "relative-path" not set`),
		},
		{
			desc:  "missing replication-factor",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path"},
			error: errors.New(`Required flag "replication-factor" not set`),
		},
		{
			desc:  "replication factor too small",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path", "-replication-factor=0"},
			error: status.Error(codes.InvalidArgument, "set replication factor: attempted to set replication factor 0 but minimum is 1"),
		},
		{
			desc:  "replication factor too big",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path", "-replication-factor=3"},
			error: status.Error(codes.InvalidArgument, "set replication factor: attempted to set replication factor 3 but virtual storage only contains 2 storages"),
		},
		{
			desc:  "virtual storage not found",
			args:  []string{"-virtual-storage=non-existent", "-relative-path=relative-path", "-replication-factor=2"},
			error: status.Error(codes.InvalidArgument, `set replication factor: virtual storage "non-existent" not found`),
		},
		{
			desc:  "repository not found",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=non-existent", "-replication-factor=2"},
			error: status.Error(codes.InvalidArgument, `set replication factor: repository "virtual-storage"/"non-existent" not found`),
		},
		{
			desc:  "assignments are disabled",
			args:  []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path", "-replication-factor=1"},
			store: praefect.NewDisabledAssignmentStore(nil),
			error: status.Error(codes.Internal, `set replication factor: assignments are disabled`),
		},
		{
			desc:   "successfully set",
			args:   []string{"-virtual-storage=virtual-storage", "-relative-path=relative-path", "-replication-factor=2"},
			stdout: "current assignments: primary, secondary\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			db.TruncateAll(t)

			store := tc.store
			if tc.store == nil {
				store = datastore.NewAssignmentStore(db, map[string][]string{"virtual-storage": {"primary", "secondary"}})
			}

			// create a repository record
			require.NoError(t,
				datastore.NewPostgresRepositoryStore(db, nil).CreateRepository(ctx, 1, "virtual-storage", "relative-path", "relative-path", "primary", nil, nil, false, false),
			)

			ln, clean := listenAndServe(t, []svcRegistrar{registerPraefectInfoServer(
				info.NewServer(config.Config{}, testhelper.NewLogger(t), nil, store, nil, nil),
			)})
			defer clean()

			conf := config.Config{
				SocketPath: ln.Addr().String(),
				VirtualStorages: []*config.VirtualStorage{
					{
						Name: "praefect",
						Nodes: []*config.Node{
							{Storage: "storage", Address: "address"},
						},
					},
				},
				DB: testdb.GetConfig(t, db.Name),
			}

			confPath := writeConfigToFile(t, conf)
			stdout, stderr, err := runApp(append([]string{"-config", confPath, setReplicationFactorCmdName}, tc.args...))
			assert.Empty(t, stderr)
			testhelper.RequireGrpcError(t, tc.error, err)
			if tc.stdout != "" {
				require.Equal(t, tc.stdout, stdout)
			}
		})
	}
}
