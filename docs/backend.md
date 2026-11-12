# How the Travel Docs backend works

A guided tour of the Go API: what it does, how a request flows through it, and
where each piece lives. Read it top to bottom once, then use it as a map.
Paths are relative to the repository root.

## 1. The big picture

The backend is one Go program (`main.go`) that serves a JSON HTTP API. It
stores everything in **PostgreSQL with PostGIS** (geometry support) and uses
**NATS** to pass small real-time messages between API instances.

```
browser (Next.js app)
   │  HTTPS + JSON, Authorization: Bearer <token>
   ▼
API (Go, Echo web framework)  ──►  PostgreSQL + PostGIS   (all data, job queue)
   │  ▲                       ──►  NATS                    (live events between instances)
   │  └── import workers (background goroutines in the same process)
   ▼
GET /events  (Server-Sent Events stream back to the browser)
```

What a user can do through it:

- **Accounts:** register, log in, refresh sessions, change password or email, delete the account.
- **Routes:** upload GPX tracks; see them as GeoJSON with an elevation profile; edit; keep a version history and restore old versions; share with other users as viewer or editor; make public.
- **Collections:** group routes and share the group.
- **Imports:** upload big GPX files that are processed in the background, with progress, cancel, retry and no duplicates.
- **Trips:** a private log of "I walked this route on this date, it took this long", with totals.
- **Activity log:** an audit trail of everything that changed.

## 2. Code layout

All library code is under `internal/` (Go forbids importing it from outside this module).

| Package | What it is |
|---|---|
| `main.go` | Sets up logging, reads config, calls `app.Run`. |
| `internal/config` | Reads environment variables into a `Config` struct and validates them. |
| `internal/app` | Wiring: opens the database and NATS, builds the server, starts workers, and shuts everything down in order. |
| `internal/server` | Builds the HTTP handler: middleware chain, error format, CORS, rate limits, and registers every domain's routes. |
| `internal/auth` | Users, passwords, JWT access tokens, refresh tokens, and the "who is calling" middleware. |
| `internal/routes` | Routes: storage, versions, sharing, GeoJSON, upload validation (`ingest.go`). |
| `internal/routes/imports` | The background import system: job queue, workers, progress. |
| `internal/collections` | Collections of routes. |
| `internal/trips` | The trip log. |
| `internal/events` | `GET /events`, the per-user live event stream (SSE). |
| `internal/pubsub` | The publish/subscribe abstraction (NATS, or in-memory for a single instance). |
| `internal/sharing` | Shared code for "share with user X as viewer/editor" (used by routes and collections). |
| `internal/audit` | Writes and lists activity-log events. |
| `internal/gpx`, `internal/routestats` | Parsing GPX XML; computing distance, ascent and the elevation profile. |
| `internal/db` | Connection pool, migrations, query helpers (`ListPage`, `Querier`). |
| `internal/httpx`, `internal/paging` | Small HTTP helpers: path IDs, `?page` parsing, If-Match handling, the page type. |
| `internal/apperr` | Typed errors (`NotFound`, `Forbidden`, …) that the server turns into HTTP status codes. |
| `internal/metrics`, `internal/tracing`, `internal/logging`, `internal/ratelimit` | Operations: Prometheus metrics, OpenTelemetry traces, structured logs, token-bucket limits. |
| `apitest/` | End-to-end tests that run the real server against a real database. |

Every domain package follows the same shape: **`model.go`** (types and
errors), **`store.go`** (SQL), **`handlers.go`** (HTTP). Handlers parse the
request and call the store; the store talks to PostgreSQL and enforces
permissions.

## 3. Life of a request

Take `GET /routes/{id}`:

1. **Middleware** (`internal/server/server.go`, in order): request ID → CORS → tracing span → request log → metrics → panic recovery → security headers → **auth** → auth-endpoint rate limit → general rate limit.
2. **Auth middleware** (`internal/auth/service.go`): if there's an `Authorization: Bearer …` header, it verifies the JWT and puts the user ID into the request context. No header means "anonymous", which is fine for public routes.
3. **Handler** (`internal/routes/handlers.go`): parses the ID (malformed IDs become 404, so they never reach the database) and calls `Store.Get(ctx, actor, id)`.
4. **Store** (`internal/routes/store.go`): runs SQL that also asks the database for the caller's role on that route through the SQL function `route_role(route, user)`. If the role is NULL, the caller may not see it, and the store returns `routes.ErrNotFound`.
5. **Errors** (`errorHandler` in `server.go`): any `apperr` error becomes its HTTP status with the body `{"error": "message"}`. Anything unexpected becomes a 500 with no details (the real error goes to the log).

**Key idea:** permissions live in the database query, not scattered through
handlers. "Not allowed to see" and "doesn't exist" both return 404 on purpose,
so nobody can probe which IDs exist.

## 4. Authentication

- **Access token:** a short-lived (15 min) JWT signed with an ECDSA key (ES256). The server only verifies the signature and expiry; no database lookup per request.
- **Refresh token:** a random secret (valid 30 days) stored *hashed* in the database. `POST /auth/refresh` swaps it for a new pair and **rotates** it. Reusing an old one means it was stolen, so the whole session family is revoked.
- **Changing your password or "log out everywhere"** also invalidates existing access tokens immediately.
- **No cookies:** the browser keeps the refresh token in localStorage and sends it in the request body.
- **Keys:** `JWT_PRIVATE_KEY_FILE` / `JWT_PUBLIC_KEY_FILE`. The committed keys in `dev/keys/` are only used with `APP_ENV=dev`; without real keys the server refuses to start, because anyone could forge tokens with public ones.

## 5. Routes, versions and sharing

- **Upload** (`POST /routes`): `ingest.go` checks that the file looks like XML, parses the GPX, and `content.go` computes stats (distance, ascent/descent, min/max elevation, point count, start time, recorded duration) and a GeoJSON MultiLineString. The geometry is stored as a PostGIS `geom` column, which makes the "only routes in this map area" filter (`ST_Intersects`) fast.
- **Versions:** every change (metadata edit, new geometry, restore) bumps `routes.version` and copies the content into `route_versions` in the same transaction. Restoring copies an old version back as a *new* version, so history is never lost.
- **Optimistic concurrency:** responses carry `ETag: "3"` (the version). Edits send `If-Match: "3"`; if someone else changed the route meanwhile, the answer is **412**. `lockForChange` in `store.go` does this check under a row lock (`SELECT … FOR UPDATE`).
- **Roles:** owner, editor (can edit and restore), viewer (read-only). Public routes are viewable by anyone. Shares live in `route_shares`, managed through `internal/sharing`.
- **Audit:** each change also writes an `audit_events` row in the same transaction (`internal/audit`). That's what the Activity dialog shows.

## 6. Background imports (the most interesting part)

Big files shouldn't block an HTTP request, so `POST /import-jobs` stores the
upload as a **job** in the `import_jobs` table and answers **202** right away.
Workers process it later. Code: `internal/routes/imports`.

1. **Enqueue** (`store.go` `Enqueue`): with an `Idempotency-Key` header, the same upload sent twice returns the existing job (header `Idempotent-Replayed: true`) instead of importing twice. A per-user advisory lock serializes this check, and each user may have at most 10 active jobs.
2. **Wake-up:** the handler publishes a `wake` message on NATS, so idle workers on *every* instance start right away. They also poll every 2 s in case the message is lost.
3. **Claim:** a worker runs `UPDATE … WHERE id = (SELECT … FOR UPDATE SKIP LOCKED LIMIT 1)`. `SKIP LOCKED` lets many workers take different jobs without blocking each other. The job gets a **lease** (owner instance plus expiry).
4. **Run** (`runner.go`): parse → analyze → save, reporting progress (stage and percent) to the database and publishing it to the owner's event stream. A **heartbeat** renews the lease every few seconds and notices cancel requests.
5. **Finish:** the route is created and the job marked `succeeded` in **one transaction**, so there's never a route without a finished job or the reverse.
6. **Failure handling:**
   - Bad file: permanent `failed` (no retry).
   - Server or database hiccup: retried with exponential backoff, up to 3 attempts.
   - Worker crashed: its lease expires, and `maintain` puts the job back in the queue.
   - Shutdown: running jobs get `IMPORT_SHUTDOWN_GRACE` to finish, then are handed back without using up an attempt.
7. **Bounded resources:** `IMPORT_WORKERS` jobs at once per instance (memory), and `UPLOAD_SLOTS` uploads read at once.

## 7. Trips

`internal/trips`. A trip is **private** to its owner: `route_id`, `trip_date`,
`duration_s`, name and notes.

- `POST /trips` with JSON `{route_id}`, or multipart with a GPX `file`. The upload path creates the route and the trip in one transaction.
- Date and duration default to the GPX recording (first and last timestamp) when the file has times.
- The trip **copies** the route's distance and ascent when it's logged, so later route edits don't rewrite history. Deleting the route sets `route_id` to NULL but keeps the trip.
- `GET /trips/stats` sums trips with SQL aggregates (`count`, `sum`, `min`, `max`), optionally per year or route.

## 8. Live events

- `GET /events` is a **Server-Sent Events** stream per user (`internal/events`). Import job updates go to topic `user.<id>`.
- `internal/pubsub` hides *how* events travel. **NATS** fans them out to all API instances, so a job running on instance B reaches a browser connected to instance A. With `PUBSUB=local`, an in-memory broker works for a single instance.
- **Delivery is best effort.** If a subscriber is too slow or NATS reconnects, the stream sends `resync` and the browser reloads its state. This trade-off keeps the system simple: the database is the source of truth, and events are just hints to refresh sooner.

## 9. Database and migrations

- SQL files in `internal/db/migrations/` are embedded in the binary and applied at startup, in order, inside one transaction guarded by an advisory lock (so several instances can start at once safely). Applied files are recorded in `schema_migrations`.
- Permission logic lives in SQL functions `route_role()` and `collection_role()` (migration 0002/0003). Most queries call them.
- Query style: fixed queries are plain SQL strings; dynamic ones (filters, sorting) use the squirrel builder via `db.SQL`. `db.ListPage` runs "count + one page + scan" for every list endpoint.

## 10. Configuration, startup and shutdown

- **Config:** environment variables only (`internal/config`; full table in `README.md`). Notable: `DATABASE_URL`, `NATS_URL` or `PUBSUB=local`, `APP_ENV`, the JWT key files, `IMPORT_WORKERS`, `RATE_LIMIT_*`, `CORS_ORIGINS`.
- **Startup** (`internal/app/run.go`): tracing → database connect and migrate → load keys → connect broker → build server → start import workers → listen.
- **Shutdown** on SIGTERM or Ctrl+C:
  1. Stop accepting connections and tell event streams to reconnect elsewhere.
  2. Let in-flight requests finish while workers finish or requeue their jobs.
  3. Close the broker, the database and tracing.
- **Operations:** JSON logs with request and trace IDs; Prometheus metrics on `METRICS_ADDR`; OpenTelemetry tracing when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; 429 with `Retry-After` from rate limits.

## 11. Tests (the best way to learn by reading)

- `make test`: starts the database container and runs everything, including `apitest/`, which boots the real server and calls it over HTTP.
- **Read these to see the API in action:**
  - `apitest/routes_test.go`: upload, versions, permissions.
  - `apitest/jobs_test.go`: retries, crashed workers, idempotency.
  - `apitest/trips_test.go`
  - `apitest/auth_test.go`: token rotation and theft detection.
- `apitest/testdata/*.golden` pin every endpoint and the JSON shape of every response. If you change the API on purpose, run `make contract-update` and review the diff.

## 12. Suggested reading order in the code

1. `internal/server/server.go`: every endpoint registered in one place.
2. `internal/routes/handlers.go` → `store.go`: one complete domain.
3. `internal/auth/service.go` and `tokens.go`.
4. `internal/routes/imports/runner.go` with `store.go`: the queue, leases and retries.
5. `internal/pubsub/pubsub.go`, then `internal/events/events.go`.
6. `internal/app/run.go`: how it all starts and stops.
