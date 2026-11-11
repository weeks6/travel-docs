-- Stage 3: GPX imports run as background jobs.
--
-- Workers claim queued jobs with FOR UPDATE SKIP LOCKED and hold a lease
-- (lease_owner, lease_expires_at) that they renew while working. Jobs whose
-- lease expires (a crashed instance) are queued again.

CREATE TABLE import_jobs (
    id               uuid PRIMARY KEY,
    owner_id         uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    -- create: new route; replace_geometry: new version of route_id.
    kind             text NOT NULL CHECK (kind IN ('create', 'replace_geometry')),
    status           text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    stage            text NOT NULL DEFAULT 'queued',
    progress         smallint NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    -- Incremented on every change, so clients can ignore stale updates.
    revision         bigint NOT NULL DEFAULT 1,

    -- Target route (replace_geometry) or the created route (create).
    route_id         uuid REFERENCES routes ON DELETE SET NULL,
    filename         text NOT NULL,
    name             text NOT NULL DEFAULT '',
    visibility       text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
    if_version       integer,

    -- Same key from the same user returns the same job instead of a new one.
    idempotency_key  text,
    -- Hash of the request (kind, target, options and file) the key was used for.
    request_hash     bytea NOT NULL,
    size_bytes       bigint NOT NULL,
    -- The uploaded file; removed once the import succeeded.
    payload          bytea,

    attempts         integer NOT NULL DEFAULT 0,
    max_attempts     integer NOT NULL DEFAULT 3,
    run_after        timestamptz NOT NULL DEFAULT now(),
    lease_owner      text,
    lease_expires_at timestamptz,
    cancel_requested boolean NOT NULL DEFAULT false,
    error            text,

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);

CREATE UNIQUE INDEX import_jobs_idempotency_key ON import_jobs (owner_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX import_jobs_queue_idx ON import_jobs (run_after, created_at) WHERE status = 'queued';
CREATE INDEX import_jobs_lease_idx ON import_jobs (lease_expires_at) WHERE status = 'running';
CREATE INDEX import_jobs_owner_idx ON import_jobs (owner_id, created_at DESC);
CREATE INDEX import_jobs_finished_idx ON import_jobs (finished_at) WHERE finished_at IS NOT NULL;
