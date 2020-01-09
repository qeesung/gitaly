package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id:   "202001091751_hello_world",
		Up:   []string{"CREATE TABLE hello_world (id integer)"},
		Down: []string{"DROP TABLE hello_world"},
	}

	allMigrations = append(allMigrations, m)
}
