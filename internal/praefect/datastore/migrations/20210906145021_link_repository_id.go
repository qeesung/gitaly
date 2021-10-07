package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20210906145021_link_repository_id",
		Up: []string{
			// Empty migration because the original implementation
			// was causing issues on large installations.
			// See https://gitlab.com/gitlab-org/gitaly/-/issues/3806
		},
		Down: []string{},
	}

	allMigrations = append(allMigrations, m)
}
