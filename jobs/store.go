package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/db"
)

// Defaults for new jobs.
const (
	DefaultMaxAttempts = 3
	// MaxActivePerUser limits queued and running jobs per user.
	MaxActivePerUser = 10
)

// errLeaseLost means another worker took over the job (or it was requeued
// after this worker stopped renewing its lease). The worker must stop
// without touching the job.
var errLeaseLost = errors.New("import job lease lost")

// errCanceled is the cancellation cause when the owner cancels a job.
var errCanceled = errors.New("import job canceled")

// Store persists import jobs.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const jobColumns = `id::text, kind, status, stage, progress, revision, route_id::text, filename, name,
	visibility, size_bytes, attempts, max_attempts, cancel_requested, error, run_after, created_at,
	updated_at, started_at, finished_at, owner_id::text, if_version`

func scanJob(row pgx.Row, extra ...any) (Job, error) {
	var j Job
	dest := []any{&j.ID, &j.Kind, &j.Status, &j.Stage, &j.Progress, &j.Revision, &j.RouteID, &j.Filename,
		&j.Name, &j.Visibility, &j.SizeBytes, &j.Attempts, &j.MaxAttempts, &j.CancelRequested, &j.Error,
		&j.RunAfter, &j.CreatedAt, &j.UpdatedAt, &j.StartedAt, &j.FinishedAt, &j.OwnerID, &j.IfVersion}
	err := row.Scan(append(dest, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// NewJob describes an upload to import.
type NewJob struct {
	OwnerID        string
	Kind           string
	RouteID        string // replace_geometry only
	IfVersion      int    // replace_geometry only; 0 = any version
	Filename       string
	Name           string
	Visibility     string
	IdempotencyKey string // optional
	Payload        []byte
}

// requestHash identifies what an Idempotency-Key was used for.
func (n NewJob) requestHash() []byte {
	file := sha256.Sum256(n.Payload)
	h := sha256.New()
	for _, s := range []string{n.Kind, n.RouteID, n.Filename, n.Name, n.Visibility} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	binary.Write(h, binary.BigEndian, int64(n.IfVersion))
	h.Write(file[:])
	return h.Sum(nil)
}

// Enqueue stores a new queued job. When the owner already used the same
// Idempotency-Key for the same upload, the existing job is returned with
// replayed = true; for a different upload it is an error.
func (s *Store) Enqueue(ctx context.Context, n NewJob) (job Job, replayed bool, err error) {
	hash := n.requestHash()
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serializes enqueues per user, for the key lookup and active limit.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "import_jobs:"+n.OwnerID); err != nil {
			return err
		}
		if n.IdempotencyKey != "" {
			var existingHash []byte
			j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+`, request_hash FROM import_jobs
				WHERE owner_id = $1 AND idempotency_key = $2`, n.OwnerID, n.IdempotencyKey), &existingHash)
			switch {
			case err == nil:
				if string(existingHash) != string(hash) {
					return errKeyReused
				}
				job, replayed = j, true
				return nil
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
		if err := checkActiveLimit(ctx, tx, n.OwnerID); err != nil {
			return err
		}
		var key, routeID, ifVersion any
		if n.IdempotencyKey != "" {
			key = n.IdempotencyKey
		}
		if n.RouteID != "" {
			routeID = n.RouteID
		}
		if n.IfVersion > 0 {
			ifVersion = n.IfVersion
		}
		job, err = scanJob(tx.QueryRow(ctx, `
			INSERT INTO import_jobs (id, owner_id, kind, route_id, if_version, filename, name, visibility,
				idempotency_key, request_hash, size_bytes, payload, max_attempts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING `+jobColumns,
			uuid.NewV7().String(), n.OwnerID, n.Kind, routeID, ifVersion, n.Filename, n.Name, n.Visibility,
			key, hash, len(n.Payload), n.Payload, DefaultMaxAttempts))
		return err
	})
	return job, replayed, err
}

func checkActiveLimit(ctx context.Context, tx pgx.Tx, owner string) error {
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM import_jobs WHERE owner_id = $1 AND status IN ('queued', 'running')`,
		owner).Scan(&active); err != nil {
		return err
	}
	if active >= MaxActivePerUser {
		return apperr.Newf(apperr.TooManyRequests,
			"you already have %d imports in progress; wait for them to finish", active)
	}
	return nil
}

// Get returns one of the owner's jobs.
func (s *Store) Get(ctx context.Context, owner, id string) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM import_jobs WHERE id = $1 AND owner_id = $2`, id, owner))
}

// List returns the owner's jobs, newest first. filter is "active",
// "finished" or "" (all).
func (s *Store) List(ctx context.Context, owner, filter string, page, pageSize int) (ListResult, error) {
	res := ListResult{Items: []Job{}, Page: page, PageSize: pageSize}
	where := sq.And{sq.Eq{"owner_id": owner}}
	switch filter {
	case "active":
		where = append(where, sq.Eq{"status": []string{StatusQueued, StatusRunning}})
	case "finished":
		where = append(where, sq.Eq{"status": []string{StatusSucceeded, StatusFailed, StatusCanceled}})
	}
	countSQL, countArgs, err := db.SQL.Select("count(*)").From("import_jobs").Where(where).ToSql()
	if err != nil {
		return res, err
	}
	if err := s.pool.QueryRow(ctx, countSQL, countArgs...).Scan(&res.Total); err != nil {
		return res, err
	}
	res.TotalPages = (res.Total + pageSize - 1) / pageSize
	listSQL, listArgs, err := db.SQL.Select(jobColumns).From("import_jobs").Where(where).
		OrderBy("created_at DESC", "id DESC").
		Limit(uint64(pageSize)).Offset(uint64((page - 1) * pageSize)).ToSql()
	if err != nil {
		return res, err
	}
	rows, err := s.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, j)
	}
	return res, rows.Err()
}

// Cancel cancels a queued job right away, or asks the worker running it to
// stop. Finished jobs can't be canceled.
func (s *Store) Cancel(ctx context.Context, owner, id string) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `
		UPDATE import_jobs SET
			cancel_requested = true,
			status      = CASE WHEN status = 'queued' THEN 'canceled' ELSE status END,
			stage       = CASE WHEN status = 'queued' THEN 'canceled' ELSE stage END,
			finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END,
			revision = revision + 1, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND status IN ('queued', 'running')
		RETURNING `+jobColumns, id, owner))
	if errors.Is(err, ErrNotFound) {
		if _, err := s.Get(ctx, owner, id); err != nil {
			return Job{}, err
		}
		return Job{}, errFinished
	}
	return j, err
}

// Retry queues a failed or canceled job again with a fresh set of attempts.
func (s *Store) Retry(ctx context.Context, owner, id string) (Job, error) {
	var j Job
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "import_jobs:"+owner); err != nil {
			return err
		}
		cur, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM import_jobs
			WHERE id = $1 AND owner_id = $2 FOR UPDATE`, id, owner))
		if err != nil {
			return err
		}
		if cur.Status != StatusFailed && cur.Status != StatusCanceled {
			return errNotRetry
		}
		if err := checkActiveLimit(ctx, tx, owner); err != nil {
			return err
		}
		j, err = scanJob(tx.QueryRow(ctx, `
			UPDATE import_jobs SET status = 'queued', stage = 'queued', progress = 0, attempts = 0,
				error = NULL, cancel_requested = false, run_after = now(), started_at = NULL,
				finished_at = NULL, revision = revision + 1, updated_at = now()
			WHERE id = $1 AND payload IS NOT NULL
			RETURNING `+jobColumns, id))
		if errors.Is(err, ErrNotFound) {
			return apperr.New(apperr.Conflict, "the uploaded file is no longer available; upload it again")
		}
		return err
	})
	return j, err
}

// Delete removes a finished job.
func (s *Store) Delete(ctx context.Context, owner, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM import_jobs
		WHERE id = $1 AND owner_id = $2 AND status IN ('succeeded', 'failed', 'canceled')`, id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, owner, id); err != nil {
			return err
		}
		return errNotFinished
	}
	return nil
}

// claim takes the next queued job for worker and returns it with its file,
// or ErrNotFound when the queue is empty.
func (s *Store) claim(ctx context.Context, worker string, lease time.Duration) (Job, []byte, error) {
	var payload []byte
	j, err := scanJob(s.pool.QueryRow(ctx, `
		UPDATE import_jobs SET status = 'running', stage = 'starting', progress = 0,
			attempts = attempts + 1, lease_owner = $1, lease_expires_at = now() + make_interval(secs => $2),
			started_at = COALESCE(started_at, now()), revision = revision + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM import_jobs
			WHERE status = 'queued' AND run_after <= now()
			ORDER BY run_after, created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED)
		RETURNING `+jobColumns+`, payload`, worker, lease.Seconds()), &payload)
	return j, payload, err
}

// heartbeat renews the worker's lease. It reports whether cancellation was
// requested, or errLeaseLost if the worker no longer owns the job.
func (s *Store) heartbeat(ctx context.Context, id, worker string, lease time.Duration) (cancelRequested bool, err error) {
	err = s.pool.QueryRow(ctx, `
		UPDATE import_jobs SET lease_expires_at = now() + make_interval(secs => $3)
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'
		RETURNING cancel_requested`, id, worker, lease.Seconds()).Scan(&cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errLeaseLost
	}
	return cancelRequested, err
}

// update changes a running job owned by worker. set is a SQL SET list using
// $3, $4, ... for args.
func (s *Store) update(ctx context.Context, id, worker, set string, args ...any) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, `
		UPDATE import_jobs SET `+set+`, revision = revision + 1, updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'
		RETURNING `+jobColumns, append([]any{id, worker}, args...)...))
	if errors.Is(err, ErrNotFound) {
		return j, errLeaseLost
	}
	return j, err
}

func (s *Store) progress(ctx context.Context, id, worker, stage string, pct int) (Job, error) {
	return s.update(ctx, id, worker, `stage = $3, progress = $4`, stage, pct)
}

// fail marks the job as failed for good.
func (s *Store) fail(ctx context.Context, id, worker, msg string) (Job, error) {
	return s.update(ctx, id, worker, `status = 'failed', stage = 'failed', error = $3, finished_at = now(),
		lease_owner = NULL, lease_expires_at = NULL`, msg)
}

// requeue schedules another attempt after delay.
func (s *Store) requeue(ctx context.Context, id, worker, msg string, delay time.Duration) (Job, error) {
	return s.update(ctx, id, worker, `status = 'queued', stage = 'queued', progress = 0, error = $3,
		run_after = now() + make_interval(secs => $4), lease_owner = NULL, lease_expires_at = NULL`,
		msg, delay.Seconds())
}

func (s *Store) markCanceled(ctx context.Context, id, worker string) (Job, error) {
	return s.update(ctx, id, worker, `status = 'canceled', stage = 'canceled', finished_at = now(),
		lease_owner = NULL, lease_expires_at = NULL`)
}

// release puts a job back into the queue without counting the attempt
// (the instance is shutting down).
func (s *Store) release(ctx context.Context, id, worker string) (Job, error) {
	return s.update(ctx, id, worker, `status = 'queued', stage = 'queued', progress = 0,
		attempts = greatest(attempts - 1, 0), run_after = now(), lease_owner = NULL, lease_expires_at = NULL`)
}

// lockForCompletion locks the job row in tx and checks that worker still
// runs it and nobody asked to cancel it. Cancel requests wait for tx.
func lockForCompletion(ctx context.Context, tx pgx.Tx, id, worker string) error {
	var status string
	var owner *string
	var cancelRequested bool
	err := tx.QueryRow(ctx, `SELECT status, lease_owner, cancel_requested FROM import_jobs WHERE id = $1 FOR UPDATE`,
		id).Scan(&status, &owner, &cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return errLeaseLost // deleted together with its owner
	}
	if err != nil {
		return err
	}
	if status != StatusRunning || owner == nil || *owner != worker {
		return errLeaseLost
	}
	if cancelRequested {
		return errCanceled
	}
	return nil
}

// completeInTx marks the job as succeeded with its route and drops the file.
func completeInTx(ctx context.Context, tx pgx.Tx, id, routeID string) (Job, error) {
	return scanJob(tx.QueryRow(ctx, `
		UPDATE import_jobs SET status = 'succeeded', stage = 'done', progress = 100, route_id = $2,
			error = NULL, payload = NULL, finished_at = now(), lease_owner = NULL, lease_expires_at = NULL,
			revision = revision + 1, updated_at = now()
		WHERE id = $1
		RETURNING `+jobColumns, id, routeID))
}

// reclaimExpired requeues running jobs whose worker stopped renewing its
// lease (e.g. the instance crashed). Jobs out of attempts fail, and jobs
// with a pending cancel request are canceled.
func (s *Store) reclaimExpired(ctx context.Context) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE import_jobs SET
			status = CASE WHEN cancel_requested THEN 'canceled'
			              WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
			stage  = CASE WHEN cancel_requested THEN 'canceled'
			              WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
			error  = CASE WHEN NOT cancel_requested AND attempts >= max_attempts
			              THEN 'the import stopped responding too many times' ELSE error END,
			finished_at = CASE WHEN cancel_requested OR attempts >= max_attempts THEN now() END,
			progress = CASE WHEN cancel_requested OR attempts >= max_attempts THEN progress ELSE 0 END,
			run_after = now(), lease_owner = NULL, lease_expires_at = NULL,
			revision = revision + 1, updated_at = now()
		WHERE status = 'running' AND lease_expires_at < now()
		RETURNING `+jobColumns)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Job, error) { return scanJob(row) })
}

// cleanup deletes jobs that finished more than retention ago.
func (s *Store) cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM import_jobs WHERE finished_at < now() - make_interval(secs => $1)`,
		retention.Seconds())
	if err != nil {
		return 0, fmt.Errorf("delete old import jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) queueDepth(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM import_jobs WHERE status = 'queued'`).Scan(&n)
	return n, err
}
