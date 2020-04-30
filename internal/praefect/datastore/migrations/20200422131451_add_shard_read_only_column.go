package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200422131451_add_shard_read_only_column",
		Up: []string{`ALTER TABLE shard_primaries
		ADD COLUMN read_only BOOLEAN NOT NULL DEFAULT false,
		ADD COLUMN demoted BOOLEAN NOT NULL DEFAULT false`},
		Down: []string{`ALTER TABLE shard_primaries
		DROP COLUMN read_only,
		DROP COLUMN demoted`},
	}

	allMigrations = append(allMigrations, m)
}
