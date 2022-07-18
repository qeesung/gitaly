//go:build !gitaly_test_sha256

package datastore

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
)

func TestMigrateStatus(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)

	config := config.Config{
		DB: testdb.GetConfig(t, db.Name),
	}

	_, err := db.Exec("INSERT INTO schema_migrations VALUES ('2020_01_01_test', NOW())")
	require.NoError(t, err)

	rows, err := MigrateStatus(config)
	require.NoError(t, err)

	m := rows["20200109161404_hello_world"]
	require.True(t, m.Migrated)
	require.False(t, m.Unknown)

	m = rows["2020_01_01_test"]
	require.True(t, m.Migrated)
	require.True(t, m.Unknown)
}
