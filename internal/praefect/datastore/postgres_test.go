package datastore

import (
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/migrations"
)

func TestMigrateStatus(t *testing.T) {
	t.Parallel()
	db := glsql.NewDB(t)

	config := config.Config{
		DB: glsql.GetDBConfig(t, db.Name),
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

func TestMigration_20211122193734_remove_cancelled_state(t *testing.T) {
	t.Parallel()
	mgs := migrations.Upto("20211122193734_remove_cancelled_state")
	require.Len(t, mgs, 38)
	// Apply all migration except the last one which is 20211122193734
	db := glsql.NewDB(t, mgs[:(len(mgs)-1)]...)

	res, err := db.DB.Exec(`INSERT INTO replication_queue(state, job) VALUES ('ready', '{}'), ('in_progress', '{}'), ('dead', '{}')`)
	require.NoError(t, err)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 3, affected)

	next, err := glsql.Migrate(db.DB, false, mgs...)
	require.NoError(t, err)
	require.Greater(t, next, 0)

	var state JobState
	require.NoError(t, db.DB.QueryRow(`SELECT state FROM replication_queue WHERE state = 'ready'::REPLICATION_JOB_STATE`).Scan(&state))
	require.Equal(t, JobStateReady, state)

	_, err = db.DB.Exec(`UPDATE replication_queue SET state = 'cancelled'`)
	var pqerr *pq.Error
	require.Truef(t, errors.As(err, &pqerr), "%+v [%T]", err, err)
	require.Equal(t, `invalid input value for enum replication_job_state: "cancelled"`, pqerr.Message)

	require.NoError(t,
		db.DB.QueryRow(`SELECT EXISTS (SELECT FROM valid_primaries LIMIT 1)`).Scan(new(bool)),
		"make sure the view exists",
	)
}
