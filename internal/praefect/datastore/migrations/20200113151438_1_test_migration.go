package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id:   "20200113151438_1_test_migration",
		Up:   []string{"INSERT INTO hello_world (id) VALUES (1)"},
		Down: []string{"DELETE FROM hello_world WHERE id = 1"},
	}

	allMigrations = append(allMigrations, m)
}
