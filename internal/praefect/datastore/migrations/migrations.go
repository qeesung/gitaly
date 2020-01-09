package migrations

import (
	migrate "github.com/rubenv/sql-migrate"
)

var allMigrations []*migrate.Migration

// All returns all migrations defined in the package
func All() []*migrate.Migration {
	return allMigrations
}
