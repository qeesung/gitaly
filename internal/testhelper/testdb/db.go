package testdb

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/migrations"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

const (
	advisoryLockIDDatabaseTemplate = 1627644550
	praefectTemplateDatabase       = "praefect_template"
)

// TxWrapper is a simple wrapper around *sql.Tx.
type TxWrapper struct {
	*sql.Tx
}

// Rollback executes Rollback operation on the wrapped *sql.Tx if it is set.
// After execution is sets Tx to nil to prevent errors on the repeated invocations (useful
// for testing when Rollback is deferred).
func (txw *TxWrapper) Rollback(tb testing.TB) {
	tb.Helper()
	if txw.Tx != nil {
		require.NoError(tb, txw.Tx.Rollback())
		txw.Tx = nil
	}
}

// Commit executes Commit operation on the wrapped *sql.Tx if it is set.
// After execution is sets Tx to nil to prevent errors on the deferred invocations (useful
// for testing when Rollback is deferred).
func (txw *TxWrapper) Commit(tb testing.TB) {
	tb.Helper()
	if txw.Tx != nil {
		require.NoError(tb, txw.Tx.Commit())
		txw.Tx = nil
	}
}

// DB is a helper struct that should be used only for testing purposes.
type DB struct {
	*sql.DB
	// Name is a name of the database.
	Name string
}

// Begin starts a new transaction and returns it wrapped into TxWrapper.
func (db DB) Begin(tb testing.TB) *TxWrapper {
	tb.Helper()
	tx, err := db.DB.Begin()
	require.NoError(tb, err)
	return &TxWrapper{Tx: tx}
}

// Truncate removes all data from the list of tables and restarts identities for them.
func (db DB) Truncate(tb testing.TB, tables ...string) {
	tb.Helper()

	for _, table := range tables {
		_, err := db.DB.Exec("DELETE FROM " + table)
		require.NoError(tb, err, "database cleanup failed: %s", tables)
	}

	_, err := db.DB.Exec("SELECT setval(relname::TEXT, 1, false) from pg_class where relkind = 'S'")
	require.NoError(tb, err, "database cleanup failed: %s", tables)
}

// RequireRowsInTable verifies that `tname` table has `n` amount of rows in it.
func (db DB) RequireRowsInTable(tb testing.TB, tname string, n int) {
	tb.Helper()

	var count int
	require.NoError(tb, db.QueryRow("SELECT COUNT(*) FROM "+tname).Scan(&count))
	require.Equal(tb, n, count, "unexpected amount of rows in table: %d instead of %d", count, n)
}

// TruncateAll removes all data from known set of tables.
func (db DB) TruncateAll(tb testing.TB) {
	db.Truncate(tb,
		"replication_queue_job_lock",
		"replication_queue",
		"replication_queue_lock",
		"node_status",
		"shard_primaries",
		"storage_repositories",
		"repositories",
		"virtual_storages",
		"repository_assignments",
		"storage_cleanups",
	)
}

// MustExec executes `q` with `args` and verifies there are no errors.
func (db DB) MustExec(tb testing.TB, q string, args ...interface{}) {
	_, err := db.DB.Exec(q, args...)
	require.NoError(tb, err)
}

// Close removes schema if it was used and releases connection pool.
func (db DB) Close() error {
	if err := db.DB.Close(); err != nil {
		return errors.New("failed to release connection pool: " + err.Error())
	}
	return nil
}

// New returns a wrapper around the database connection pool.
// Must be used only for testing.
// The new database with empty relations will be created for each call of this function.
// It uses env vars:
//   PGHOST - required, URL/socket/dir
//   PGPORT - required, binding port
//   PGUSER - optional, user - `$ whoami` would be used if not provided
// Once the test is completed the database will be dropped on test cleanup execution.
func New(tb testing.TB) DB {
	tb.Helper()
	database := "praefect_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	return DB{DB: initPraefectDB(tb, database), Name: database}
}

// GetConfig returns the database configuration determined by
// environment variables.  See NewDB() for the list of variables.
func GetConfig(tb testing.TB, database string) config.DB {
	env := getDatabaseEnvironment(tb)

	require.Contains(tb, env, "PGHOST", "PGHOST env var expected to be provided to connect to Postgres database")
	require.Contains(tb, env, "PGPORT", "PGHOST env var expected to be provided to connect to Postgres database")

	portNumber, err := strconv.Atoi(env["PGPORT"])
	require.NoError(tb, err, "PGPORT must be a port number of the Postgres database listens for incoming connections")

	// connect to 'postgres' database first to re-create testing database from scratch
	conf := config.DB{
		Host:    env["PGHOST"],
		Port:    portNumber,
		DBName:  database,
		SSLMode: "disable",
		User:    env["PGUSER"],
		SessionPooled: config.DBConnection{
			Host: env["PGHOST"],
			Port: portNumber,
		},
	}

	if bouncerHost, ok := env["PGHOST_PGBOUNCER"]; ok {
		conf.Host = bouncerHost
	}

	if bouncerPort, ok := env["PGPORT_PGBOUNCER"]; ok {
		bouncerPortNumber, err := strconv.Atoi(bouncerPort)
		require.NoError(tb, err, "PGPORT_PGBOUNCER must be a port number of the PgBouncer")
		conf.Port = bouncerPortNumber
	}

	return conf
}

func requireSQLOpen(tb testing.TB, dbCfg config.DB, direct bool) *sql.DB {
	tb.Helper()
	db, err := sql.Open("pgx", glsql.DSN(dbCfg, direct))
	require.NoErrorf(tb, err, "failed to connect to %q database", dbCfg.DBName)
	if !assert.NoErrorf(tb, db.Ping(), "failed to communicate with %q database", dbCfg.DBName) {
		require.NoErrorf(tb, db.Close(), "release connection to the %q database", dbCfg.DBName)
	}
	return db
}

func requireTerminateAllConnections(tb testing.TB, db *sql.DB, database string) {
	tb.Helper()
	_, err := db.Exec("SELECT PG_TERMINATE_BACKEND(pid) FROM PG_STAT_ACTIVITY WHERE datname = '" + database + "'")
	require.NoError(tb, err)

	// Once the pg_terminate_backend has completed, we may need to wait before the connections
	// are fully released. pg_terminate_backend will return true as long as the signal was
	// sent successfully, but the backend needs to respond to the signal to close the connection.
	// TODO: In Postgre 14, pg_terminate_backend takes an optional timeout argument that makes it a blocking
	// call. https://gitlab.com/gitlab-org/gitaly/-/issues/3937 tracks the refactor work to  remove this
	// require.Eventuallyf call in favor of passing in a timeout to pg_terminate_backend
	require.Eventuallyf(tb, func() bool {
		var openConnections int
		require.NoError(tb, db.QueryRow(
			`SELECT COUNT(*) FROM pg_stat_activity
				WHERE datname = $1 AND pid != pg_backend_pid()`, database).
			Scan(&openConnections))
		return openConnections == 0
	}, 20*time.Second, 10*time.Millisecond, "wait for all connections to be terminated")
}

func initPraefectDB(tb testing.TB, database string) *sql.DB {
	tb.Helper()

	dbCfg := GetConfig(tb, "postgres")
	// We require a direct connection to the Postgres instance and not through the PgBouncer
	// because we use transaction pool mood for it and it doesn't work well for system advisory locks.
	postgresDB := requireSQLOpen(tb, dbCfg, true)
	defer func() {
		require.NoErrorf(tb, postgresDB.Close(), "release connection to the %q database", dbCfg.DBName)
	}()

	// Acquire exclusive advisory lock to prevent other concurrent test from doing the same.
	_, err := postgresDB.Exec(`SELECT pg_advisory_lock($1)`, advisoryLockIDDatabaseTemplate)
	require.NoError(tb, err, "not able to acquire lock for synchronisation")
	var advisoryUnlock func()
	advisoryUnlock = func() {
		require.True(tb, scanSingleBool(tb, postgresDB, `SELECT pg_advisory_unlock($1)`, advisoryLockIDDatabaseTemplate), "release advisory lock")
		advisoryUnlock = func() {}
	}
	defer func() { advisoryUnlock() }()

	templateDBExists := databaseExist(tb, postgresDB, praefectTemplateDatabase)
	if !templateDBExists {
		_, err := postgresDB.Exec("CREATE DATABASE " + praefectTemplateDatabase + " WITH ENCODING 'UTF8'")
		require.NoErrorf(tb, err, "failed to create %q database", praefectTemplateDatabase)
	}

	templateDBConf := GetConfig(tb, praefectTemplateDatabase)
	templateDB := requireSQLOpen(tb, templateDBConf, true)
	defer func() {
		require.NoErrorf(tb, templateDB.Close(), "release connection to the %q database", templateDBConf.DBName)
	}()

	if _, err := glsql.Migrate(templateDB, false); err != nil {
		// If database has unknown migration we try to re-create template database with
		// current migration. It may be caused by other code changes done in another branch.
		if pErr := (*migrate.PlanError)(nil); errors.As(err, &pErr) {
			if strings.EqualFold(pErr.ErrorMessage, "unknown migration in database") {
				require.NoErrorf(tb, templateDB.Close(), "release connection to the %q database", templateDBConf.DBName)

				_, err = postgresDB.Exec("DROP DATABASE " + praefectTemplateDatabase)
				require.NoErrorf(tb, err, "failed to drop %q database", praefectTemplateDatabase)
				_, err = postgresDB.Exec("CREATE DATABASE " + praefectTemplateDatabase + " WITH ENCODING 'UTF8'")
				require.NoErrorf(tb, err, "failed to create %q database", praefectTemplateDatabase)

				remigrateTemplateDB := requireSQLOpen(tb, templateDBConf, true)
				defer func() {
					require.NoErrorf(tb, remigrateTemplateDB.Close(), "release connection to the %q database", templateDBConf.DBName)
				}()
				_, err = glsql.Migrate(remigrateTemplateDB, false)
				require.NoErrorf(tb, err, "failed to run database migration on %q", praefectTemplateDatabase)
			} else {
				require.NoErrorf(tb, err, "failed to run database migration on %q", praefectTemplateDatabase)
			}
		} else {
			require.NoErrorf(tb, err, "failed to run database migration on %q", praefectTemplateDatabase)
		}
	}

	// Release advisory lock as soon as possible to unblock other tests from execution.
	advisoryUnlock()

	require.NoErrorf(tb, templateDB.Close(), "release connection to the %q database", templateDBConf.DBName)

	_, err = postgresDB.Exec(`CREATE DATABASE ` + database + ` TEMPLATE ` + praefectTemplateDatabase)
	require.NoErrorf(tb, err, "failed to create %q database", praefectTemplateDatabase)

	tb.Cleanup(func() {
		if _, ok := getDatabaseEnvironment(tb)["PGHOST_PGBOUNCER"]; ok {
			pgbouncerCfg := dbCfg
			// This database name will connect us to the special admin console.
			pgbouncerCfg.DBName = "pgbouncer"

			// We cannot use `requireSQLOpen()` because it would ping the database,
			// which is not supported by the PgBouncer admin console.
			pgbouncerDB, err := sql.Open("pgx", glsql.DSN(pgbouncerCfg, false))
			require.NoError(tb, err)
			defer testhelper.MustClose(tb, pgbouncerDB)

			// Trying to release connections like we do with the "normal" Postgres
			// database regularly results in flaky tests with PgBouncer given that the
			// connections are seemingly never released. Instead, we kill PgBouncer
			// connections by connecting to its admin console and using the KILL
			// command, which instructs it to kill all client and server connections.
			_, err = pgbouncerDB.Exec("KILL " + database)
			require.NoError(tb, err, "killing PgBouncer connections")
		}

		dbCfg.DBName = "postgres"
		postgresDB := requireSQLOpen(tb, dbCfg, true)
		defer testhelper.MustClose(tb, postgresDB)

		// We need to force-terminate open connections as for the tasks that use PgBouncer
		// the actual client connected to the database is a PgBouncer and not a test that is
		// running.
		requireTerminateAllConnections(tb, postgresDB, database)

		_, err = postgresDB.Exec("DROP DATABASE " + database)
		require.NoErrorf(tb, err, "failed to drop %q database", database)
	})

	// Connect to the testing database with optional PgBouncer
	dbCfg.DBName = database
	praefectTestDB := requireSQLOpen(tb, dbCfg, false)
	tb.Cleanup(func() {
		if err := praefectTestDB.Close(); !errors.Is(err, net.ErrClosed) {
			require.NoErrorf(tb, err, "release connection to the %q database", dbCfg.DBName)
		}
	})
	return praefectTestDB
}

func databaseExist(tb testing.TB, db *sql.DB, database string) bool {
	return scanSingleBool(tb, db, `SELECT EXISTS(SELECT * FROM pg_database WHERE datname = $1)`, database)
}

func scanSingleBool(tb testing.TB, db *sql.DB, query string, args ...interface{}) bool {
	var flag bool
	row := db.QueryRow(query, args...)
	require.NoError(tb, row.Scan(&flag))
	return flag
}

var (
	// Running `gdk env` takes about 250ms on my system and is thus comparatively slow. When
	// running with Praefect as proxy, this time adds up and may thus slow down tests by quite a
	// margin. We thus amortize these costs by only running it once.
	databaseEnvOnce sync.Once
	databaseEnv     map[string]string
)

func getDatabaseEnvironment(tb testing.TB) map[string]string {
	databaseEnvOnce.Do(func() {
		envvars := map[string]string{}

		// We only process output if `gdk env` returned success. If it didn't, we simply assume that
		// we are not running in a GDK environment and will try to extract variables from the
		// environment instead.
		if output, err := exec.Command("gdk", "env").Output(); err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				const prefix = "export "
				if !strings.HasPrefix(line, prefix) {
					continue
				}

				split := strings.SplitN(strings.TrimPrefix(line, prefix), "=", 2)
				if len(split) != 2 {
					continue
				}

				envvars[split[0]] = split[1]
			}
		}

		for _, key := range []string{"PGHOST", "PGPORT", "PGUSER", "PGHOST_PGBOUNCER", "PGPORT_PGBOUNCER"} {
			if _, ok := envvars[key]; !ok {
				value, ok := os.LookupEnv(key)
				if ok {
					envvars[key] = value
				}
			}
		}

		databaseEnv = envvars
	})

	return databaseEnv
}

// WaitForBlockedQuery is a helper that waits until a blocked query matching the prefix is present in the
// database. This is useful for ensuring another transaction is blocking a query when testing concurrent
// execution of multiple queries.
func WaitForBlockedQuery(ctx context.Context, tb testing.TB, db glsql.Querier, queryPrefix string) {
	tb.Helper()

	for {
		var queryBlocked bool
		require.NoError(tb, db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT FROM pg_stat_activity
				WHERE TRIM(e'\n' FROM query) LIKE $1
				AND state = 'active'
				AND wait_event_type = 'Lock'
				AND datname = current_database()
			)
		`, queryPrefix+"%").Scan(&queryBlocked))

		if queryBlocked {
			return
		}

		retry := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			retry.Stop()
			return
		case <-retry.C:
		}
	}
}

// SetMigrations ensures the requested number of migrations are up and the remainder are down.
func SetMigrations(tb testing.TB, db DB, cfg config.Config, up int) {
	// Ensure all migrations are up first
	_, err := glsql.Migrate(db.DB, true)
	require.NoError(tb, err)

	migrationCt := len(migrations.All())

	if up < migrationCt {
		down := migrationCt - up

		migrationSet := migrate.MigrationSet{
			TableName: migrations.MigrationTableName,
		}
		ms := &migrate.MemoryMigrationSource{Migrations: migrations.All()}

		// It would be preferable to use migrate.MigrateDown() here, but that introduces
		// a circular dependency between testdb and datastore.
		n, err := migrationSet.ExecMax(db.DB, "postgres", ms, migrate.Down, down)
		require.NoError(tb, err)
		require.Equal(tb, down, n)
	}
}
