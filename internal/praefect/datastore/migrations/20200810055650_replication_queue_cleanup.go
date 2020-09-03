package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200810055650_replication_queue_cleanup",
		Up: []string{
			`DELETE FROM replication_queue WHERE state = ANY (ARRAY['dead'::REPLICATION_JOB_STATE, 'completed'::REPLICATION_JOB_STATE])`,
		},
		Down: []string{
			// there is no way to restore deleted data so far
		},
	}

	allMigrations = append(allMigrations, m)
}
