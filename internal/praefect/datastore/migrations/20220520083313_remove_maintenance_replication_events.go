package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20220520083313_remove_maintenance_replication_events",
		Up: []string{
			`DELETE FROM replication_queue WHERE job->>'change' IN (
				'gc',
				'repack_full',
				'repack_incremental',
				'cleanup',
				'pack_refs',
				'write_commit_graph',
				'midx_repack',
				'optimize_repository',
				'prune_unreachable_objects'
			)`,
		},
		Down: []string{
			// We cannot get this data back anymore.
		},
	}

	allMigrations = append(allMigrations, m)
}
