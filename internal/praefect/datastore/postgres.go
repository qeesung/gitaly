package datastore

import (
	"context"
	"fmt"
	"sync"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/migrations"
)

// MigrationStatusRow represents an entry in the schema migrations table.
// If the migration is in the database but is not listed, Unknown will be true.
type MigrationStatusRow struct {
	Migrated  bool
	Unknown   bool
	AppliedAt time.Time
}

var (
	postgresVersion     int
	postgresVersionErr  error
	postgresVersionOnce sync.Once
)

// CheckPostgresVersion checks the server version of the Postgres DB
// specified in conf. This is a diagnostic for the Praefect Postgres
// rollout. https://gitlab.com/gitlab-org/gitaly/issues/1755
func CheckPostgresVersion(db glsql.Querier) error {
	serverVersion, err := getPostgresVersion(db)
	if err != nil {
		return err
	}

	const minimumServerVersion = 9_06_00 // Postgres 9.6
	if serverVersion < minimumServerVersion {
		return fmt.Errorf("postgres server version too old: %d", serverVersion)
	}

	return nil
}

// GetPostgresVersion retrieves the version of the Postgres database server. Note that this function
// lazily checks the version once and once only: if the database server connected to changes during
// runtime, then the version won't be reevaluated.
func getPostgresVersion(db glsql.Querier) (int, error) {
	postgresVersionOnce.Do(func() {
		postgresVersion, postgresVersionErr = func() (int, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			var serverVersion int
			if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&serverVersion); err != nil {
				return 0, fmt.Errorf("get postgres server version: %v", err)
			}

			return serverVersion, nil
		}()
	})

	return postgresVersion, postgresVersionErr
}

// supportsMaterialized determines whether the given database supports the MATERIALIZED keyword.
func supportsMaterialized(db glsql.Querier) (bool, error) {
	serverVersion, err := getPostgresVersion(db)
	if err != nil {
		return false, err
	}

	// MATERIALIZE is supported starting with Postgres 12.0.
	return serverVersion >= 12_00_00, nil
}

const sqlMigrateDialect = "postgres"

// MigrateDownPlan does a dry run for rolling back at most max migrations.
func MigrateDownPlan(conf config.Config, max int) ([]string, error) {
	db, err := glsql.OpenDB(conf.DB)
	if err != nil {
		return nil, fmt.Errorf("sql open: %v", err)
	}
	defer db.Close()

	planned, _, err := migrate.PlanMigration(db, sqlMigrateDialect, migrationSource(), migrate.Down, max)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, m := range planned {
		result = append(result, m.Id)
	}

	return result, nil
}

// MigrateDown rolls back at most max migrations.
func MigrateDown(conf config.Config, max int) (int, error) {
	db, err := glsql.OpenDB(conf.DB)
	if err != nil {
		return 0, fmt.Errorf("sql open: %v", err)
	}
	defer db.Close()

	return migrate.ExecMax(db, sqlMigrateDialect, migrationSource(), migrate.Down, max)
}

// MigrateStatus returns the status of database migrations. The key of the map
// indexes the migration ID.
func MigrateStatus(conf config.Config) (map[string]*MigrationStatusRow, error) {
	db, err := glsql.OpenDB(conf.DB)
	if err != nil {
		return nil, fmt.Errorf("sql open: %v", err)
	}
	defer db.Close()

	migrations, err := migrationSource().FindMigrations()
	if err != nil {
		return nil, err
	}

	records, err := migrate.GetMigrationRecords(db, sqlMigrateDialect)
	if err != nil {
		return nil, err
	}

	rows := make(map[string]*MigrationStatusRow)

	for _, m := range migrations {
		rows[m.Id] = &MigrationStatusRow{
			Migrated: false,
		}
	}

	for _, r := range records {
		if rows[r.Id] == nil {
			rows[r.Id] = &MigrationStatusRow{
				Unknown: true,
			}
		}

		rows[r.Id].Migrated = true
		rows[r.Id].AppliedAt = r.AppliedAt
	}

	return rows, nil
}

func migrationSource() *migrate.MemoryMigrationSource {
	return &migrate.MemoryMigrationSource{Migrations: migrations.All()}
}
