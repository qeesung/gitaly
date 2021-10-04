package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20210906145021_link_repository_id",
		Up: []string{
			`
DROP TRIGGER notify_on_update ON storage_repositories
			`,
			`
CREATE TRIGGER notify_on_update
AFTER UPDATE OF virtual_storage, relative_path, storage, generation ON storage_repositories
REFERENCING OLD TABLE AS OLD NEW TABLE AS NEW
FOR EACH STATEMENT
EXECUTE FUNCTION notify_on_change('storage_repositories_updates')
			`,
			`
UPDATE storage_repositories
SET repository_id = repositories.repository_id
FROM repositories
WHERE storage_repositories.virtual_storage = repositories.virtual_storage
AND   storage_repositories.relative_path   = repositories.relative_path
			`,
			`
UPDATE repository_assignments
SET repository_id = repositories.repository_id
FROM repositories
WHERE repository_assignments.virtual_storage = repositories.virtual_storage
AND   repository_assignments.relative_path   = repositories.relative_path
			`,
		},
		Down: []string{},
	}

	allMigrations = append(allMigrations, m)
}
