package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200707101830_repositories_table",
		Up: []string{`
CREATE TABLE repositories (
	virtual_storage TEXT,
	relative_path TEXT,
	generation BIGINT NOT NULL,
	PRIMARY KEY (virtual_storage, relative_path)
)`, `
CREATE TABLE storage_repositories (
    virtual_storage TEXT,
    relative_path TEXT,
    storage TEXT,
    generation BIGINT NOT NULL,
    PRIMARY KEY (virtual_storage, relative_path, storage)
)
`},
		Down: []string{
			"DROP TABLE storage_repositories",
			"DROP TABLE repositories",
		},
	}

	allMigrations = append(allMigrations, m)
}
