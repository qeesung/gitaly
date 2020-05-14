package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200512131219_replication_job_indexing",
		Up: []string{
			`CREATE INDEX IF NOT EXISTS virtual_target_on_replication_queue_idx
				ON replication_queue USING BTREE ((job->>'virtual_storage'), (job->>'target_node_storage'))`,
		},
		Down: []string{
			`DROP INDEX IF EXISTS virtual_target_on_replication_queue_idx`,
		},
	}

	allMigrations = append(allMigrations, m)
}
