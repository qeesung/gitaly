package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id:   "20200602154246_remember_previous_writable_primary",
		Up:   []string{"ALTER TABLE shard_primaries ADD COLUMN previous_writable_primary TEXT"},
		Down: []string{"ALTER TABLE shard_primaries DROP COLUMN previous_writable_primary"},
	}

	allMigrations = append(allMigrations, m)
}
