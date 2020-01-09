package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id:   "20200109161404_hello_world",
		Up:   []string{"CREATE TABLE hello_world (id integer)"},
		Down: []string{"DROP TABLE hello_world"},
	}

	allMigrations = append(allMigrations, m)
}
