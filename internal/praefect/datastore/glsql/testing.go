package glsql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
)

var (
	// testDB is a shared database connection pool that needs to be used only for testing.
	// Initialization of it happens on the first call to GetDB and it remains open until call to Clean.
	testDB         DB
	testDBInitOnce sync.Once
)

// DB is a helper struct that should be used only for testing purposes.
type DB struct {
	*sql.DB
}

// Truncate removes all data from the list of tables and restarts identities for them.
func (db DB) Truncate(t testing.TB, tables ...string) {
	t.Helper()

	for _, table := range tables {
		_, err := db.DB.Exec("DELETE FROM " + table)
		require.NoError(t, err, "database cleanup failed: %s", tables)
	}

	_, err := db.DB.Exec("SELECT setval(relname::TEXT, 1, false) from pg_class where relkind = 'S'")
	require.NoError(t, err, "database cleanup failed: %s", tables)
}

// RequireRowsInTable verifies that `tname` table has `n` amount of rows in it.
func (db DB) RequireRowsInTable(t *testing.T, tname string, n int) {
	t.Helper()

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+tname).Scan(&count))
	require.Equal(t, n, count, "unexpected amount of rows in table: %d instead of %d", count, n)
}

// TruncateAll removes all data from known set of tables.
func (db DB) TruncateAll(t testing.TB) {
	db.Truncate(t,
		"replication_queue_job_lock",
		"replication_queue",
		"replication_queue_lock",
		"node_status",
		"shard_primaries",
		"storage_repositories",
		"repositories",
		"virtual_storages",
	)
}

// MustExec executes `q` with `args` and verifies there are no errors.
func (db DB) MustExec(t testing.TB, q string, args ...interface{}) {
	_, err := db.DB.Exec(q, args...)
	require.NoError(t, err)
}

// Close removes schema if it was used and releases connection pool.
func (db DB) Close() error {
	if err := db.DB.Close(); err != nil {
		return errors.New("failed to release connection pool: " + err.Error())
	}
	return nil
}

// GetDB returns a wrapper around the database connection pool.
// Must be used only for testing.
// The new `database` will be re-created for each package that uses this function.
// Each call will also truncate all tables with their identities restarted if any.
// The best place to call it is in individual testing functions.
// It uses env vars:
//   PGHOST - required, URL/socket/dir
//   PGPORT - required, binding port
//   PGUSER - optional, user - `$ whoami` would be used if not provided
func GetDB(t testing.TB, database string) DB {
	t.Helper()

	testDBInitOnce.Do(func() {
		sqlDB := initGitalyTestDB(t, database)

		_, mErr := Migrate(sqlDB, false)
		require.NoError(t, mErr, "failed to run database migration")
		testDB = DB{DB: sqlDB}
	})

	testDB.TruncateAll(t)

	return testDB
}

// GetDBConfig returns the database configuration determined by
// environment variables.  See GetDB() for the list of variables.
func GetDBConfig(t testing.TB, database string) config.DB {
	getEnvFromGDK(t)

	host, hostFound := os.LookupEnv("PGHOST")
	require.True(t, hostFound, "PGHOST env var expected to be provided to connect to Postgres database")

	port, portFound := os.LookupEnv("PGPORT")
	require.True(t, portFound, "PGPORT env var expected to be provided to connect to Postgres database")
	portNumber, pErr := strconv.Atoi(port)
	require.NoError(t, pErr, "PGPORT must be a port number of the Postgres database listens for incoming connections")

	// connect to 'postgres' database first to re-create testing database from scratch
	conf := config.DB{
		Host:    host,
		Port:    portNumber,
		DBName:  database,
		SSLMode: "disable",
		User:    os.Getenv("PGUSER"),
		SessionPooled: config.DBConnection{
			Host: host,
			Port: portNumber,
		},
	}

	bouncerHost, bouncerHostFound := os.LookupEnv("PGHOST_PGBOUNCER")
	if bouncerHostFound {
		conf.Host = bouncerHost
	}

	bouncerPort, bouncerPortFound := os.LookupEnv("PGPORT_PGBOUNCER")
	if bouncerPortFound {
		bouncerPortNumber, pErr := strconv.Atoi(bouncerPort)
		require.NoError(t, pErr, "PGPORT_PGBOUNCER must be a port number of the PgBouncer")

		conf.Port = bouncerPortNumber
	}

	return conf
}

func initGitalyTestDB(t testing.TB, database string) *sql.DB {
	t.Helper()

	dbCfg := GetDBConfig(t, "postgres")

	postgresDB, oErr := OpenDB(dbCfg)
	require.NoError(t, oErr, "failed to connect to 'postgres' database")
	defer func() { require.NoError(t, postgresDB.Close()) }()

	_, tErr := postgresDB.Exec("SELECT PG_TERMINATE_BACKEND(pid) FROM PG_STAT_ACTIVITY WHERE datname = '" + database + "'")
	require.NoError(t, tErr)

	_, dErr := postgresDB.Exec("DROP DATABASE IF EXISTS " + database)
	require.NoErrorf(t, dErr, "failed to drop %q database", database)

	_, cErr := postgresDB.Exec("CREATE DATABASE " + database + " WITH ENCODING 'UTF8'")
	require.NoErrorf(t, cErr, "failed to create %q database", database)
	require.NoError(t, postgresDB.Close(), "error on closing connection to 'postgres' database")

	// connect to the testing database
	dbCfg.DBName = database
	gitalyTestDB, err := OpenDB(dbCfg)
	require.NoErrorf(t, err, "failed to connect to %q database", database)
	return gitalyTestDB
}

// Clean removes created schema if any and releases DB connection pool.
// It needs to be called only once after all tests for package are done.
// The best place to use it is TestMain(*testing.M) {...} after m.Run().
func Clean() error {
	if testDB.DB != nil {
		return testDB.Close()
	}
	return nil
}

func getEnvFromGDK(t testing.TB) {
	gdkEnv, err := exec.Command("gdk", "env").Output()
	if err != nil {
		// Assume we are not in a GDK setup; this is not an error so just return.
		return
	}

	for _, line := range strings.Split(string(gdkEnv), "\n") {
		const prefix = "export "
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		split := strings.SplitN(strings.TrimPrefix(line, prefix), "=", 2)
		if len(split) != 2 {
			continue
		}
		key, value := split[0], split[1]

		require.NoError(t, os.Setenv(key, value), "set env var %v", key)
	}
}

// PostgresContainer provides access to the SQL connection pool and a port number on which the
// database accepts new connections.
type PostgresContainer struct {
	*sql.DB
	Port  string
	close func() error
}

// Close should be called to stop running container.
func (pc PostgresContainer) Close() error {
	return pc.close()
}

// RunPostgres starts a brand new container of the Postgres database and runs migrations on top of it.
func RunPostgres() (PostgresContainer, error) {
	const (
		database = "testcontainer"
		exposed  = nat.Port("5432/tcp")
	)
	waitSQL := wait.ForSQL(exposed, "postgres", func(port nat.Port) string { return "user=postgres sslmode=disable port=" + port.Port() })
	waitSQL.Timeout(time.Second * 30)

	req := testcontainers.ContainerRequest{
		Image:        "postgres:11.6-alpine",
		ExposedPorts: []string{string(exposed)},
		WaitingFor:   waitSQL,
		Env:          map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"},
		AutoRemove:   true,
	}

	ctx := context.Background()
	postgesCtnr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return PostgresContainer{}, err
	}

	terminate := func() error {
		return postgesCtnr.Terminate(ctx)
	}

	port, err := postgesCtnr.MappedPort(ctx, exposed)
	if err != nil {
		_ = terminate()
		return PostgresContainer{}, err
	}

	dbCfg := config.DB{
		Host:    "localhost",
		Port:    port.Int(),
		DBName:  "postgres",
		User:    "postgres",
		SSLMode: "disable",
		SessionPooled: config.DBConnection{
			Host: "localhost",
			Port: port.Int(),
		},
	}

	postgresDB, err := OpenDB(dbCfg)
	if err != nil {
		_ = terminate()
		return PostgresContainer{}, err
	}

	if _, err := postgresDB.Exec("CREATE DATABASE " + database + " WITH ENCODING 'UTF8'"); err != nil {
		_ = terminate()
		return PostgresContainer{}, err
	}

	if err := postgresDB.Close(); err != nil {
		_ = terminate()
		return PostgresContainer{}, err
	}

	dbCfg.DBName = database
	db, err := OpenDB(dbCfg)
	if err != nil {
		_ = terminate()
		return PostgresContainer{}, err
	}

	clean := func() error {
		if err := db.Close(); err != nil {
			_ = terminate()
			return err
		}
		return terminate()
	}

	if _, err := Migrate(db, false); err != nil {
		_ = clean()
		return PostgresContainer{}, err
	}

	return PostgresContainer{DB: db, Port: port.Port(), close: clean}, nil
}
