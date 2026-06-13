// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package state

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq" // Postgres driver

	"github.com/MediaMolder/MediaMolder/job"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// PostgresStore is the Phase C state-store adapter backed by lib/pq.
type PostgresStore struct {
	db *sql.DB
}

// OpenPostgres opens a Postgres state-store at dsn, running all pending
// schema migrations on first use. The DSN must be a valid lib/pq connection
// string, e.g. "postgres://user:pass@host/dbname?sslmode=disable".
func OpenPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("state: open postgres %q: %w", dsn, err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	s := &PostgresStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state: postgres migration: %w", err)
	}
	return s, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

// migrate applies all unapplied SQL migration files in lexical order.
func (s *PostgresStore) migrate(ctx context.Context) error {
	// Ensure schema_migrations table exists first.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, e := range entries {
		version := strings.TrimSuffix(e.Name(), ".sql")
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if _, err := s.db.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, version,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

// ---- Job operations -------------------------------------------------------

func (s *PostgresStore) CreateJob(ctx context.Context, j job.Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("state: marshal job: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO jobs(id, doc_json) VALUES($1,$2)`,
		j.ID, string(b),
	); err != nil {
		return fmt.Errorf("state: insert job: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO job_statuses(job_id, status) VALUES($1,$2)`,
		j.ID, string(JobStatusQueued),
	); err != nil {
		return fmt.Errorf("state: insert job status: %w", err)
	}
	return tx.Commit()
}

func (s *PostgresStore) GetJob(ctx context.Context, id string) (job.Job, JobStatusRecord, error) {
	var docJSON string
	if err := s.db.QueryRowContext(ctx,
		`SELECT doc_json FROM jobs WHERE id=$1`, id,
	).Scan(&docJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return job.Job{}, JobStatusRecord{}, fmt.Errorf("state: job %q not found", id)
		}
		return job.Job{}, JobStatusRecord{}, err
	}
	var j job.Job
	if err := json.Unmarshal([]byte(docJSON), &j); err != nil {
		return job.Job{}, JobStatusRecord{}, fmt.Errorf("state: unmarshal job: %w", err)
	}

	var status, errMsg string
	var updatedAt time.Time
	if err := s.db.QueryRowContext(ctx,
		`SELECT status, error, updated_at FROM job_statuses WHERE job_id=$1`, id,
	).Scan(&status, &errMsg, &updatedAt); err != nil {
		return job.Job{}, JobStatusRecord{}, err
	}
	rec := JobStatusRecord{
		Status:    JobStatus(status),
		Error:     errMsg,
		UpdatedAt: updatedAt,
	}
	return j, rec, nil
}

func (s *PostgresStore) UpdateJobStatus(ctx context.Context, jobID string, rec JobStatusRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_statuses(job_id, status, error, updated_at) VALUES($1,$2,$3,$4)
		 ON CONFLICT(job_id) DO UPDATE SET status=EXCLUDED.status, error=EXCLUDED.error, updated_at=EXCLUDED.updated_at`,
		jobID, string(rec.Status), rec.Error, rec.UpdatedAt,
	)
	return err
}

// ---- Event log ------------------------------------------------------------

func (s *PostgresStore) AppendEvent(ctx context.Context, e JobEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_events(job_id, type, data_json) VALUES($1,$2,$3)`,
		e.JobID, e.Type, e.DataJSON,
	)
	return err
}

func (s *PostgresStore) ListEvents(ctx context.Context, jobID string, afterID int64) ([]JobEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, data_json, created_at FROM job_events WHERE job_id=$1 AND id>$2 ORDER BY id`,
		jobID, afterID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evts []JobEvent
	for rows.Next() {
		var e JobEvent
		if err := rows.Scan(&e.ID, &e.Type, &e.DataJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.JobID = jobID
		evts = append(evts, e)
	}
	return evts, rows.Err()
}

// ---- Task management ------------------------------------------------------

func (s *PostgresStore) UpsertTask(ctx context.Context, t job.Task, status TaskStatus) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("state: marshal task: %w", err)
	}
	var leaseUntil *time.Time
	if !t.LeaseUntil.IsZero() {
		leaseUntil = &t.LeaseUntil
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tasks(id, job_id, stage_id, doc_json, status, lease_until) VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT(id) DO UPDATE SET doc_json=EXCLUDED.doc_json, status=EXCLUDED.status,
		                                lease_until=EXCLUDED.lease_until, updated_at=NOW()`,
		t.ID, t.JobID, t.StageID, string(b), string(status), leaseUntil,
	)
	return err
}

func (s *PostgresStore) SetTaskResult(ctx context.Context, taskID string, r job.TaskResult) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("state: marshal task result: %w", err)
	}
	status := TaskStatusSucceeded
	if r.Error != "" {
		status = TaskStatusFailed
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE tasks SET result_json=$1, status=$2, updated_at=NOW() WHERE id=$3`,
		string(b), string(status), taskID,
	)
	return err
}

func (s *PostgresStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	return s.pgScanTask(s.db.QueryRowContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks WHERE id=$1`, taskID,
	))
}

func (s *PostgresStore) TasksByStage(ctx context.Context, jobID, stageID string) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks
		 WHERE job_id=$1 AND stage_id=$2 ORDER BY created_at`,
		jobID, stageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.pgScanTaskRows(rows)
}

func (s *PostgresStore) ListTasks(ctx context.Context, jobID string) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks
		 WHERE job_id=$1 ORDER BY created_at`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.pgScanTaskRows(rows)
}

// ---- Lease management -----------------------------------------------------

func (s *PostgresStore) RenewTaskLease(ctx context.Context, taskID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET lease_until=$1, updated_at=NOW() WHERE id=$2`,
		until, taskID,
	)
	return err
}

func (s *PostgresStore) ListExpiredLeases(ctx context.Context) ([]TaskRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT doc_json, status, result_json, lease_until, updated_at FROM tasks
		 WHERE status=$1 AND lease_until IS NOT NULL AND lease_until < NOW()
		 ORDER BY created_at`,
		string(TaskStatusRunning),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.pgScanTaskRows(rows)
}

// ---- Dead-letter queue ----------------------------------------------------

func (s *PostgresStore) DeadLetterTask(ctx context.Context, taskID, reason string) error {
	rec, err := s.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rec.Task)
	if err != nil {
		return fmt.Errorf("state: marshal task for dlq: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO dead_letter_tasks(id, job_id, stage_id, task_json, reason, attempt)
		 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,
		taskID, rec.Task.JobID, rec.Task.StageID, string(b), reason, rec.Task.Attempt,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET status=$1, updated_at=NOW() WHERE id=$2`,
		string(TaskStatusFailed), taskID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListDeadLetterTasks(ctx context.Context, jobID string) ([]DeadLetterRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, job_id, stage_id, task_json, reason, attempt, created_at
		 FROM dead_letter_tasks WHERE job_id=$1 ORDER BY created_at`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadLetterRecord
	for rows.Next() {
		var r DeadLetterRecord
		if err := rows.Scan(&r.TaskID, &r.JobID, &r.StageID, &r.TaskJSON, &r.Reason, &r.Attempt, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Advisory lock (ReconcilerLocker) -------------------------------------

// TryReconcilerLock acquires a Postgres session-level advisory lock (key 42001).
// Returns a non-nil release function, true, nil on success.
// Returns a no-op release function, false, nil when another instance holds it.
func (s *PostgresStore) TryReconcilerLock(ctx context.Context) (func(context.Context), bool, error) {
	var acquired bool
	if err := s.db.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(42001)`).Scan(&acquired); err != nil {
		return func(context.Context) {}, false, fmt.Errorf("state: advisory lock: %w", err)
	}
	if !acquired {
		return func(context.Context) {}, false, nil
	}
	release := func(ctx context.Context) {
		_, _ = s.db.ExecContext(ctx, `SELECT pg_advisory_unlock(42001)`)
	}
	return release, true, nil
}

// ---- Scan helpers ---------------------------------------------------------

type pgRowScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) pgScanTask(row pgRowScanner) (TaskRecord, error) {
	var docJSON, statusStr string
	var resultJSON sql.NullString
	var leaseUntil sql.NullTime
	var updatedAt time.Time
	if err := row.Scan(&docJSON, &statusStr, &resultJSON, &leaseUntil, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskRecord{}, fmt.Errorf("state: task not found")
		}
		return TaskRecord{}, err
	}
	var t job.Task
	if err := json.Unmarshal([]byte(docJSON), &t); err != nil {
		return TaskRecord{}, fmt.Errorf("state: unmarshal task: %w", err)
	}
	if leaseUntil.Valid {
		t.LeaseUntil = leaseUntil.Time
	}
	rec := TaskRecord{
		Task:       t,
		Status:     TaskStatus(statusStr),
		LeaseUntil: t.LeaseUntil,
		UpdatedAt:  updatedAt,
	}
	if resultJSON.Valid && resultJSON.String != "" {
		var r job.TaskResult
		if err := json.Unmarshal([]byte(resultJSON.String), &r); err != nil {
			return TaskRecord{}, fmt.Errorf("state: unmarshal task result: %w", err)
		}
		rec.Result = &r
	}
	return rec, nil
}

func (s *PostgresStore) pgScanTaskRows(rows *sql.Rows) ([]TaskRecord, error) {
	var out []TaskRecord
	for rows.Next() {
		var docJSON, statusStr string
		var resultJSON sql.NullString
		var leaseUntil sql.NullTime
		var updatedAt time.Time
		if err := rows.Scan(&docJSON, &statusStr, &resultJSON, &leaseUntil, &updatedAt); err != nil {
			return nil, err
		}
		var t job.Task
		if err := json.Unmarshal([]byte(docJSON), &t); err != nil {
			return nil, fmt.Errorf("state: unmarshal task: %w", err)
		}
		if leaseUntil.Valid {
			t.LeaseUntil = leaseUntil.Time
		}
		rec := TaskRecord{
			Task:       t,
			Status:     TaskStatus(statusStr),
			LeaseUntil: t.LeaseUntil,
			UpdatedAt:  updatedAt,
		}
		if resultJSON.Valid && resultJSON.String != "" {
			var r job.TaskResult
			if err := json.Unmarshal([]byte(resultJSON.String), &r); err != nil {
				return nil, fmt.Errorf("state: unmarshal task result: %w", err)
			}
			rec.Result = &r
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
