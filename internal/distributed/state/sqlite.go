// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"github.com/MediaMolder/MediaMolder/pipeline"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS jobs (
    id         TEXT    PRIMARY KEY,
    doc_json   TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS job_statuses (
    job_id     TEXT    PRIMARY KEY REFERENCES jobs(id),
    status     TEXT    NOT NULL,
    error      TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id          TEXT    PRIMARY KEY,
    job_id      TEXT    NOT NULL REFERENCES jobs(id),
    stage_id    TEXT    NOT NULL,
    doc_json    TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    result_json TEXT,
    lease_until INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_job_stage ON tasks(job_id, stage_id);

CREATE TABLE IF NOT EXISTS job_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT    NOT NULL REFERENCES jobs(id),
    type       TEXT    NOT NULL,
    data_json  TEXT    NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_job ON job_events(job_id, id);

CREATE TABLE IF NOT EXISTS dead_letter_tasks (
    id         TEXT    PRIMARY KEY,
    job_id     TEXT    NOT NULL REFERENCES jobs(id),
    stage_id   TEXT    NOT NULL,
    task_json  TEXT    NOT NULL,
    reason     TEXT    NOT NULL,
    attempt    INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dlq_job ON dead_letter_tasks(job_id);
`

// upgradeAlterStatements lists ALTER TABLE statements run after schema creation
// to add columns introduced in Phase C. Each statement is run individually so
// that "duplicate column name" errors (SQLite < 3.37 has no IF NOT EXISTS) can
// be silently ignored — they just mean the column already exists.
var upgradeAlterStatements = []string{
	`ALTER TABLE tasks ADD COLUMN lease_until INTEGER NOT NULL DEFAULT 0`,
}

// SQLiteStore is the Phase B state-store adapter backed by mattn/go-sqlite3.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) a SQLite state-store at dsn.
// Use ":memory:" for tests.
func OpenSQLite(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dsn+"?_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("state: open sqlite %q: %w", dsn, err)
	}
	// SQLite with WAL is safe for concurrent readers + one writer.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: apply schema: %w", err)
	}
	for _, stmt := range upgradeAlterStatements {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			_ = db.Close()
			return nil, fmt.Errorf("state: apply upgrade migrations: %w", err)
		}
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// ---- Job operations -------------------------------------------------------

func (s *SQLiteStore) CreateJob(ctx context.Context, j pipeline.Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("state: marshal job: %w", err)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO jobs(id, doc_json, created_at) VALUES (?,?,?)`,
		j.ID, string(b), now,
	); err != nil {
		return fmt.Errorf("state: insert job: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO job_statuses(job_id, status, error, updated_at) VALUES (?,?,?,?)`,
		j.ID, string(JobStatusQueued), "", now,
	); err != nil {
		return fmt.Errorf("state: insert job status: %w", err)
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetJob(ctx context.Context, id string) (pipeline.Job, JobStatusRecord, error) {
	var docJSON string
	if err := s.db.QueryRowContext(ctx,
		`SELECT doc_json FROM jobs WHERE id=?`, id,
	).Scan(&docJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return pipeline.Job{}, JobStatusRecord{}, fmt.Errorf("state: job %q not found", id)
		}
		return pipeline.Job{}, JobStatusRecord{}, err
	}
	var j pipeline.Job
	if err := json.Unmarshal([]byte(docJSON), &j); err != nil {
		return pipeline.Job{}, JobStatusRecord{}, fmt.Errorf("state: unmarshal job: %w", err)
	}

	var status, errMsg string
	var updatedAtMs int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT status, error, updated_at FROM job_statuses WHERE job_id=?`, id,
	).Scan(&status, &errMsg, &updatedAtMs); err != nil {
		return pipeline.Job{}, JobStatusRecord{}, err
	}
	rec := JobStatusRecord{
		Status:    JobStatus(status),
		Error:     errMsg,
		UpdatedAt: time.UnixMilli(updatedAtMs),
	}
	return j, rec, nil
}

func (s *SQLiteStore) UpdateJobStatus(ctx context.Context, jobID string, rec JobStatusRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_statuses(job_id, status, error, updated_at) VALUES (?,?,?,?)
         ON CONFLICT(job_id) DO UPDATE SET status=excluded.status, error=excluded.error, updated_at=excluded.updated_at`,
		jobID, string(rec.Status), rec.Error, rec.UpdatedAt.UnixMilli(),
	)
	return err
}

// ---- Event log ------------------------------------------------------------

func (s *SQLiteStore) AppendEvent(ctx context.Context, e JobEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_events(job_id, type, data_json, created_at) VALUES (?,?,?,?)`,
		e.JobID, e.Type, e.DataJSON, time.Now().UnixMilli(),
	)
	return err
}

func (s *SQLiteStore) ListEvents(ctx context.Context, jobID string, afterID int64) ([]JobEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, data_json, created_at FROM job_events WHERE job_id=? AND id>? ORDER BY id`,
		jobID, afterID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []JobEvent
	for rows.Next() {
		var e JobEvent
		var createdAtMs int64
		if err := rows.Scan(&e.ID, &e.Type, &e.DataJSON, &createdAtMs); err != nil {
			return nil, err
		}
		e.JobID = jobID
		e.CreatedAt = time.UnixMilli(createdAtMs)
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

// ---- Task management ------------------------------------------------------

func (s *SQLiteStore) UpsertTask(ctx context.Context, t pipeline.Task, status TaskStatus) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("state: marshal task: %w", err)
	}
	now := time.Now().UnixMilli()
	leaseUntil := t.LeaseUntil.UnixMilli()
	if t.LeaseUntil.IsZero() {
		leaseUntil = 0
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tasks(id, job_id, stage_id, doc_json, status, lease_until, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)
         ON CONFLICT(id) DO UPDATE SET doc_json=excluded.doc_json, status=excluded.status, lease_until=excluded.lease_until, updated_at=excluded.updated_at`,
		t.ID, t.JobID, t.StageID, string(b), string(status), leaseUntil, now, now,
	)
	return err
}

func (s *SQLiteStore) SetTaskResult(ctx context.Context, taskID string, r pipeline.TaskResult) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("state: marshal task result: %w", err)
	}
	status := TaskStatusSucceeded
	if r.Error != "" {
		status = TaskStatusFailed
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET result_json=?, status=?, updated_at=? WHERE id=?`,
		string(b), string(status), time.Now().UnixMilli(), taskID,
	)
	return err
}

func (s *SQLiteStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	return s.scanTask(s.db.QueryRowContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks WHERE id=?`, taskID,
	))
}

func (s *SQLiteStore) TasksByStage(ctx context.Context, jobID, stageID string) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks WHERE job_id=? AND stage_id=? ORDER BY rowid`,
		jobID, stageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanTaskRows(rows)
}

func (s *SQLiteStore) ListTasks(ctx context.Context, jobID string) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks WHERE job_id=? ORDER BY rowid`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanTaskRows(rows)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteStore) scanTask(row rowScanner) (TaskRecord, error) {
	var docJSON string
	var statusStr string
	var resultJSON sql.NullString
	var leaseUntilMs int64
	var updatedAtMs int64
	if err := row.Scan(&docJSON, &statusStr, &resultJSON, &leaseUntilMs, &updatedAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskRecord{}, fmt.Errorf("state: task not found")
		}
		return TaskRecord{}, err
	}
	var t pipeline.Task
	if err := json.Unmarshal([]byte(docJSON), &t); err != nil {
		return TaskRecord{}, fmt.Errorf("state: unmarshal task: %w", err)
	}
	if leaseUntilMs > 0 {
		t.LeaseUntil = time.UnixMilli(leaseUntilMs)
	}
	rec := TaskRecord{
		Task:       t,
		Status:     TaskStatus(statusStr),
		LeaseUntil: t.LeaseUntil,
		UpdatedAt:  time.UnixMilli(updatedAtMs),
	}
	if resultJSON.Valid && resultJSON.String != "" {
		var r pipeline.TaskResult
		if err := json.Unmarshal([]byte(resultJSON.String), &r); err != nil {
			return TaskRecord{}, fmt.Errorf("state: unmarshal task result: %w", err)
		}
		rec.Result = &r
	}
	return rec, nil
}

func (s *SQLiteStore) scanTaskRows(rows *sql.Rows) ([]TaskRecord, error) {
	var out []TaskRecord
	for rows.Next() {
		var docJSON string
		var statusStr string
		var resultJSON sql.NullString
		var leaseUntilMs int64
		var updatedAtMs int64
		if err := rows.Scan(&docJSON, &statusStr, &resultJSON, &leaseUntilMs, &updatedAtMs); err != nil {
			return nil, err
		}
		var t pipeline.Task
		if err := json.Unmarshal([]byte(docJSON), &t); err != nil {
			return nil, fmt.Errorf("state: unmarshal task: %w", err)
		}
		if leaseUntilMs > 0 {
			t.LeaseUntil = time.UnixMilli(leaseUntilMs)
		}
		rec := TaskRecord{
			Task:       t,
			Status:     TaskStatus(statusStr),
			LeaseUntil: t.LeaseUntil,
			UpdatedAt:  time.UnixMilli(updatedAtMs),
		}
		if resultJSON.Valid && resultJSON.String != "" {
			var r pipeline.TaskResult
			if err := json.Unmarshal([]byte(resultJSON.String), &r); err != nil {
				return nil, fmt.Errorf("state: unmarshal task result: %w", err)
			}
			rec.Result = &r
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ---- Phase C additions ----------------------------------------------------

func (s *SQLiteStore) RenewTaskLease(ctx context.Context, taskID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET lease_until=?, updated_at=? WHERE id=?`,
		until.UnixMilli(), time.Now().UnixMilli(), taskID,
	)
	return err
}

func (s *SQLiteStore) ListExpiredLeases(ctx context.Context) ([]TaskRecord, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks
         WHERE status=? AND lease_until > 0 AND lease_until < ? ORDER BY rowid`,
		string(TaskStatusRunning), now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanTaskRows(rows)
}

func (s *SQLiteStore) DeadLetterTask(ctx context.Context, taskID, reason string) error {
	rec, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rec.Task)
	if err != nil {
		return fmt.Errorf("state: marshal task for dlq: %w", err)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO dead_letter_tasks(id, job_id, stage_id, task_json, reason, attempt, created_at)
         VALUES (?,?,?,?,?,?,?)
         ON CONFLICT(id) DO NOTHING`,
		taskID, rec.Task.JobID, rec.Task.StageID, string(b), reason, rec.Task.Attempt, now,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status=?, updated_at=? WHERE id=?`,
		string(TaskStatusFailed), now, taskID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListDeadLetterTasks(ctx context.Context, jobID string) ([]DeadLetterRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, job_id, stage_id, task_json, reason, attempt, created_at
         FROM dead_letter_tasks WHERE job_id=? ORDER BY created_at`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadLetterRecord
	for rows.Next() {
		var r DeadLetterRecord
		var createdAtMs int64
		if err := rows.Scan(&r.TaskID, &r.JobID, &r.StageID, &r.TaskJSON, &r.Reason, &r.Attempt, &createdAtMs); err != nil {
			return nil, err
		}
		r.CreatedAt = time.UnixMilli(createdAtMs)
		out = append(out, r)
	}
	return out, rows.Err()
}
