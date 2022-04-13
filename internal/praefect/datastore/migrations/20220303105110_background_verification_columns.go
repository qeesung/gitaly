package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20220303105110_background_verification_columns",
		Up: []string{
			"ALTER TABLE storage_repositories ADD COLUMN verified_at TIMESTAMPTZ",
			"ALTER TABLE storage_repositories ADD COLUMN verification_leased_until TIMESTAMPTZ",
			`CREATE INDEX verification_queue ON storage_repositories ( verified_at NULLS FIRST )
			        WHERE verification_leased_until IS NULL
			`,
		},
		Down: []string{
			"DROP INDEX verification_queue",
			"ALTER TABLE storage_repositories DROP COLUMN verification_leased_until",
			"ALTER TABLE storage_repositories DROP COLUMN verified_at",
		},
	}

	allMigrations = append(allMigrations, m)
}
