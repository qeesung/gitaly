package migrations

import (
	migrate "github.com/rubenv/sql-migrate"
)

const migrationTableName = "schema_migrations"

var allMigrations []*migrate.Migration

func init() {
	migrate.SetTable(migrationTableName)
}

// All returns all migrations defined in the package
func All() []*migrate.Migration {
	return allMigrations
}
