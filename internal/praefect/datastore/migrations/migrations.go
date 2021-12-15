package migrations

import (
	"strings"

	migrate "github.com/rubenv/sql-migrate"
)

// MigrationTableName is the name of the SQL table used to store migration info.
const MigrationTableName = "schema_migrations"

var allMigrations []*migrate.Migration

// All returns all migrations defined in the package
func All() []*migrate.Migration {
	return allMigrations
}

// Upto returns all migrations up to and including the migration that has provided id.
// If there is no migration with id all migrations will be returned.
func Upto(id string) []*migrate.Migration {
	var mgs []*migrate.Migration
	for _, mg := range All() {
		mgs = append(mgs, mg)
		if strings.EqualFold(mg.Id, id) {
			break
		}
	}
	return mgs
}
