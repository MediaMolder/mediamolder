-- 001_initial.sql — baseline schema for the Postgres state adapter.
-- Applied once on first OpenPostgres; subsequent opens are idempotent.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT        PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS jobs (
    id         TEXT        PRIMARY KEY,
    doc_json   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS job_statuses (
    job_id     TEXT        PRIMARY KEY REFERENCES jobs(id),
    status     TEXT        NOT NULL,
    error      TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT        PRIMARY KEY,
    job_id      TEXT        NOT NULL REFERENCES jobs(id),
    stage_id    TEXT        NOT NULL,
    doc_json    TEXT        NOT NULL,
    status      TEXT        NOT NULL,
    result_json TEXT,
    lease_until TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tasks_job_stage ON tasks(job_id, stage_id);
CREATE INDEX IF NOT EXISTS idx_tasks_expired   ON tasks(status, lease_until)
    WHERE status = 'running';

CREATE TABLE IF NOT EXISTS job_events (
    id         BIGSERIAL   PRIMARY KEY,
    job_id     TEXT        NOT NULL REFERENCES jobs(id),
    type       TEXT        NOT NULL,
    data_json  TEXT        NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_events_job ON job_events(job_id, id);

CREATE TABLE IF NOT EXISTS dead_letter_tasks (
    id         TEXT        PRIMARY KEY,
    job_id     TEXT        NOT NULL REFERENCES jobs(id),
    stage_id   TEXT        NOT NULL,
    task_json  TEXT        NOT NULL,
    reason     TEXT        NOT NULL,
    attempt    INTEGER     NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dlq_job ON dead_letter_tasks(job_id);
