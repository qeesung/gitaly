package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20220404131105_stale_verification_lease_index",
		Up: []string{`
			CREATE INDEX verification_leases ON storage_repositories (verification_leased_until)
				WHERE verification_leased_until IS NOT NULL
		`},
		Down: []string{"DROP INDEX verification_leases"},
	}

	allMigrations = append(allMigrations, m)
}
