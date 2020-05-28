package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200527103816_drop_old_gitaly_tables",
		Up: []string{
			`DROP TABLE IF EXISTS gitaly_replication_queue_job_lock CASCADE`,
			`DROP TABLE IF EXISTS gitaly_replication_queue CASCADE`,
			`DROP TABLE IF EXISTS gitaly_replication_queue_lock CASCADE`,
		},
		Down: []string{},
	}

	allMigrations = append(allMigrations, m)
}
