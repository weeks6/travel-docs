// Package imports imports GPX files in the background.
//
// An upload is stored as an import job and answered right away with 202
// Accepted. A pool of workers (Runner) claims queued jobs from the database,
// parses and analyzes the file while reporting progress, and creates the
// route (or a new route version) in the same transaction that marks the job
// as succeeded. Jobs can be canceled, are retried after transient errors,
// survive restarts and crashed instances, and are deduplicated with an
// Idempotency-Key.
package imports

import (
	"time"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
)

// Job kinds.
const (
	KindCreate          = "create"           // creates a new route
	KindReplaceGeometry = "replace_geometry" // new geometry version of route_id
)

// Job statuses.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// Processing stages reported while running, in order.
const (
	StageQueued    = "queued"
	StageStarting  = "starting"
	StageParsing   = "parsing"
	StageAnalyzing = "analyzing"
	StageSaving    = "saving"
	StageDone      = "done"
)

// Job is an import job as returned by the API.
type Job struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	Stage           string     `json:"stage"`
	Progress        int        `json:"progress"` // 0–100
	Revision        int64      `json:"revision"` // increases with every change
	RouteID         *string    `json:"route_id"`
	Filename        string     `json:"filename"`
	Name            string     `json:"name"`
	Visibility      string     `json:"visibility"`
	SizeBytes       int64      `json:"size_bytes"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	CancelRequested bool       `json:"cancel_requested"`
	Error           *string    `json:"error"`
	RunAfter        time.Time  `json:"run_after"` // earliest start of the next attempt
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`

	OwnerID   string `json:"-"`
	IfVersion *int   `json:"-"`
}

// Finished reports whether the job reached a final status.
func (j Job) Finished() bool {
	return j.Status == StatusSucceeded || j.Status == StatusFailed || j.Status == StatusCanceled
}

// ListResult is one page of jobs.
type ListResult = paging.Page[Job]

// Event is published on the owner's user topic whenever a job changes.
type Event struct {
	Type string `json:"type"` // EventJob
	Job  Job    `json:"job"`
}

// EventJob is the Type of job events.
const EventJob = "job"

// control is published on the "jobs" topic to wake workers on every
// instance or cancel a running job wherever it runs.
type control struct {
	Op string `json:"op"` // "wake" or "cancel"
	ID string `json:"id,omitempty"`
}

// ControlTopic is the broker topic for worker control messages.
const ControlTopic = "jobs"

var (
	ErrNotFound    = apperr.New(apperr.NotFound, "import job not found")
	errFinished    = apperr.New(apperr.Conflict, "the import job has already finished")
	errNotFinished = apperr.New(apperr.Conflict, "the import job is still queued or running; cancel it first")
	errNotRetry    = apperr.New(apperr.Conflict, "only failed or canceled import jobs can be retried")
	errKeyReused   = apperr.New(apperr.Unprocessable, "this Idempotency-Key was already used for a different upload")
)
