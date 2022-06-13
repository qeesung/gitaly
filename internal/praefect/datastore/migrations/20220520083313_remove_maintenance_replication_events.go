package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20220520083313_remove_maintenance_replication_events",
		Up: []string{
			`
-- Find all jobs which are maintenance-style jobs first.
WITH maintenance_job AS (
    SELECT id FROM replication_queue WHERE job->>'change' IN (
        'gc',
        'repack_full',
        'repack_incremental',
        'cleanup',
        'pack_refs',
        'write_commit_graph',
        'midx_repack',
        'optimize_repository',
        'prune_unreachable_objects'
    )
),

-- Now we have to prune the job locks before deleting the maintenance job
-- itself because the lock has a reference on the job.
deleted_maintenance_job_lock AS (
    DELETE FROM replication_queue_job_lock
    WHERE job_id IN (SELECT id FROM maintenance_job)
    RETURNING lock_id
),

-- With job locks having been deleted we can now delete the maintenance jobs.
deleted_maintenance_job AS (
    DELETE FROM replication_queue
    WHERE id IN (SELECT id FROM maintenance_job)
)

-- Finally, we need to release replication queue locks in case we have removed
-- all jobs which kept the lock.
UPDATE replication_queue_lock
SET acquired = FALSE
WHERE id IN (
    SELECT existing.lock_id
    -- We do so by unlocking all locks where the count of deleted job locks is
    -- the same as the count of existing job locks. If there happen to be any
    -- other jobs we haven't deleted then the lock stays acquired. This is the
    -- same logic as used in AcknowledgeStale.
    FROM (
        SELECT lock_id, COUNT(*) AS amount
        FROM deleted_maintenance_job_lock
        GROUP BY lock_id
    ) AS removed
    JOIN (
        SELECT lock_id, COUNT(*) AS amount
        FROM replication_queue_job_lock
        WHERE lock_id IN (select lock_id FROM deleted_maintenance_job_lock)
        GROUP BY lock_id
    ) AS existing ON removed.lock_id = existing.lock_id AND removed.amount = existing.amount
)`,
		},
		Down: []string{
			// We cannot get this data back anymore.
		},
	}

	allMigrations = append(allMigrations, m)
}
