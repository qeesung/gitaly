package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	// This migration drops 'cancelled' value from the REPLICATION_JOB_STATE enum type.
	m := &migrate.Migration{
		Id: "20211122193734_remove_cancelled_state",
		Up: []string{
			`ALTER TYPE REPLICATION_JOB_STATE RENAME TO REPLICATION_JOB_STATE_OLD`,

			`CREATE TYPE REPLICATION_JOB_STATE AS ENUM('ready', 'in_progress', 'completed', 'failed', 'dead')`,

			`ALTER TABLE replication_queue ALTER COLUMN state DROP DEFAULT`,

			// The drop is required and can't be simplified to REPLACE.
			// [2BP01] ERROR: cannot drop column state of table replication_queue because
			// other objects depend on it Detail: view valid_primaries depends on column
			// state of table replication_queue
			// Hint: Use DROP ... CASCADE to drop the dependent objects too.
			`DROP VIEW valid_primaries`,

			// The type conversion from REPLICATION_JOB_STATE_OLD to REPLICATION_JOB_STATE
			// is not possible that is why a separate column is used for migration.
			`ALTER TABLE replication_queue ADD COLUMN state_new TEXT`,

			`UPDATE replication_queue set state_new = state::TEXT`,

			`ALTER TABLE replication_queue ALTER COLUMN state_new TYPE REPLICATION_JOB_STATE USING state_new::REPLICATION_JOB_STATE`,

			// The delete_replica_unique_index, replication_queue_target_index relies on
			// the state column. They will be dropped automatically.
			`ALTER TABLE replication_queue
				ALTER COLUMN state_new SET DEFAULT 'ready'::REPLICATION_JOB_STATE,
				ALTER COLUMN state_new SET NOT NULL,
				DROP COLUMN state`,

			`ALTER TABLE replication_queue RENAME COLUMN state_new TO state`,

			// The view needs to be re-created because the type of the column it uses was changed.
			`CREATE VIEW valid_primaries AS
			SELECT repository_id, virtual_storage, relative_path, storage
			FROM (
				SELECT
					repository_id,
					repositories.virtual_storage,
					repositories.relative_path,
					storage,
					repository_assignments.storage IS NOT NULL
						OR bool_and(repository_assignments.storage IS NULL) OVER (PARTITION BY repository_id) AS eligible
				FROM repositories
				JOIN (SELECT repository_id, storage, generation FROM storage_repositories) AS storage_repositories USING (repository_id, generation)
				JOIN healthy_storages USING (virtual_storage, storage)
				LEFT JOIN repository_assignments USING (repository_id, storage)
				WHERE NOT EXISTS (
					SELECT FROM replication_queue
					WHERE state NOT IN ('completed', 'dead')
						AND job->>'change' = 'delete_replica'
						AND (job->>'repository_id')::bigint = repository_id
						AND job->>'target_node_storage' = storage
				)
			) AS candidates
			WHERE eligible`,

			// We need to re-create indexes after state column type is changed.
			`CREATE UNIQUE INDEX delete_replica_unique_index
			ON replication_queue (
				(job->>'virtual_storage'),
				(job->>'relative_path')
			)
			WHERE state NOT IN ('completed', 'dead')
			AND job->>'change' = 'delete_replica'`,

			`CREATE INDEX replication_queue_target_index
			ON replication_queue (
				(job->>'virtual_storage'),
				(job->>'relative_path'),
				(job->>'target_node_storage'),
				(job->>'change')
			)
			WHERE state NOT IN ('completed', 'dead')`,

			`DROP TYPE REPLICATION_JOB_STATE_OLD RESTRICT`,
		},
		// The down migration is not needed because there are no rows with 'cancelled' state
		// and this state was not in use for quite long period (if it was ever).
		Down: []string{
			`DROP VIEW valid_primaries`,

			`ALTER TYPE REPLICATION_JOB_STATE RENAME TO REPLICATION_JOB_STATE_OLD`,

			`CREATE TYPE REPLICATION_JOB_STATE AS ENUM('ready', 'in_progress', 'completed', 'failed', 'dead', 'cancelled')`,

			`ALTER TABLE replication_queue ALTER COLUMN state DROP DEFAULT`,

			`ALTER TABLE replication_queue ADD COLUMN state_new TEXT`,

			`UPDATE replication_queue set state_new = state::TEXT`,

			`ALTER TABLE replication_queue ALTER COLUMN state_new TYPE REPLICATION_JOB_STATE USING state_new::REPLICATION_JOB_STATE`,

			`ALTER TABLE replication_queue
				ALTER COLUMN state_new SET DEFAULT 'ready'::REPLICATION_JOB_STATE,
				ALTER COLUMN state_new SET NOT NULL,
				DROP COLUMN state`,

			`ALTER TABLE replication_queue RENAME COLUMN state_new TO state`,

			`CREATE VIEW valid_primaries AS
			SELECT repository_id, virtual_storage, relative_path, storage
			FROM (
				SELECT
					repository_id,
					repositories.virtual_storage,
					repositories.relative_path,
					storage,
					repository_assignments.storage IS NOT NULL
						OR bool_and(repository_assignments.storage IS NULL) OVER (PARTITION BY repository_id) AS eligible
				FROM repositories
				JOIN (SELECT repository_id, storage, generation FROM storage_repositories) AS storage_repositories USING (repository_id, generation)
				JOIN healthy_storages USING (virtual_storage, storage)
				LEFT JOIN repository_assignments USING (repository_id, storage)
				WHERE NOT EXISTS (
					SELECT FROM replication_queue
					WHERE state NOT IN ('completed', 'dead', 'cancelled')
						AND job->>'change' = 'delete_replica'
						AND (job->>'repository_id')::bigint = repository_id
						AND job->>'target_node_storage' = storage
				)
			) AS candidates
			WHERE eligible`,

			`CREATE UNIQUE INDEX delete_replica_unique_index
			ON replication_queue (
				(job->>'virtual_storage'),
				(job->>'relative_path')
			)
			WHERE state NOT IN ('completed', 'cancelled', 'dead')
			AND job->>'change' = 'delete_replica'`,

			`CREATE INDEX replication_queue_target_index
			ON replication_queue (
				(job->>'virtual_storage'),
				(job->>'relative_path'),
				(job->>'target_node_storage'),
				(job->>'change')
			)
			WHERE state NOT IN ('completed', 'cancelled', 'dead')`,

			`DROP TYPE REPLICATION_JOB_STATE_OLD RESTRICT`,
		},
	}

	allMigrations = append(allMigrations, m)
}
