package migrations

import migrate "github.com/rubenv/sql-migrate"

func init() {
	m := &migrate.Migration{
		Id: "20200224220728_job_queue",
		Up: []string{`
CREATE TYPE GITALY_REPLICATION_JOB_STATE AS ENUM('ready', 'in_progress', 'completed', 'cancelled', 'failed')
`, `
CREATE TABLE gitaly_replication_queue_lock
(
    id TEXT PRIMARY KEY
  , acquired BOOLEAN NOT NULL DEFAULT FALSE
)
`, `
CREATE TABLE gitaly_replication_queue
(
    id BIGSERIAL PRIMARY KEY
  , state GITALY_REPLICATION_JOB_STATE NOT NULL DEFAULT 'ready'
  , created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (NOW() AT TIME ZONE 'UTC')
  , updated_at TIMESTAMP WITHOUT TIME ZONE
  , attempt INTEGER NOT NULL DEFAULT 3
  , lock_id TEXT
  , job JSONB
  , meta JSONB
)`, `
CREATE TABLE gitaly_replication_queue_job_lock
(
    job_id BIGINT REFERENCES gitaly_replication_queue(id)
  , lock_id TEXT REFERENCES gitaly_replication_queue_lock(id)
  , triggered_at TIMESTAMP WITHOUT TIME ZONE NOT NULL DEFAULT (NOW() AT TIME ZONE 'UTC')
  , CONSTRAINT gitaly_replication_queue_job_lock_pk PRIMARY KEY (job_id, lock_id)
)`,
		},
		Down: []string{`
DROP TABLE IF EXISTS gitaly_replication_queue_job_lock CASCADE
`, `
DROP TABLE IF EXISTS gitaly_replication_queue CASCADE
`, `
DROP TABLE IF EXISTS gitaly_replication_queue_lock CASCADE
`,
		},
	}

	allMigrations = append(allMigrations, m)
}
