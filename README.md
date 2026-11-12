## Overview

- **API** (Go, this directory): routes, collections, background GPX imports, and a personal trip log (when you went on a route and how long it took).
- **Web UI** (Next.js, [`frontend/`](frontend/)): talks to the API directly from the browser (CORS).
- **PostgreSQL + PostGIS**: all data and the import job queue.
- **NATS**: real-time event fan-out between API instances (core pub/sub; best effort, clients resync after a drop).

## Running

Quickest way during development:

```sh
scripts/dev.sh --seed --open
```

It starts Docker and the database if needed, runs the API with live reload (when [air](https://github.com/air-verse/air) is installed) and the Next.js dev server, creates a demo account (`demo@example.com` / `demo-password`) with a sample route, and opens the web UI at http://localhost:3000. The API is at http://localhost:8080. Ctrl+C stops both. See `scripts/dev.sh --help` for options (`--no-web` runs only the API).

Locally by hand (Go and Node.js installed):

```sh
docker compose up -d db nats            # PostgreSQL 17 + PostGIS (also creates travel_docs_test), NATS
LOG_FORMAT=text go run .                # API on http://localhost:8080; migrations run on startup
cd frontend && npm ci && npm run dev    # web UI on http://localhost:3000
```

Everything in Docker:

```sh
docker compose up --build   # database, NATS, API (8080, metrics on 9090) and web UI (3000)
docker compose watch        # same, and rebuilds when files change
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 docker compose --profile tracing up --build   # + Jaeger at http://localhost:16686
```

The web UI's API address is built into its bundle: set `NEXT_PUBLIC_API_URL` when building it (default `http://localhost:8080`), and allow the web UI's origin on the API with `CORS_ORIGINS`.

### Configuration (API)

| Variable | Default | |
|---|---|---|
| `DATABASE_URL` | `postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable` | |
| `ADDR` | `:8080` | API listen address |
| `METRICS_ADDR` | `:9090` | Prometheus `/metrics` on a separate port; `off` disables it |
| `CORS_ORIGINS` | `http://localhost:3000` | Comma-separated browser origins allowed to call the API; `*` allows any |
| `APP_ENV` | `production` | `dev` allows the committed development JWT keys (`scripts/dev.sh` sets it) |
| `JWT_PRIVATE_KEY_FILE` / `JWT_PUBLIC_KEY_FILE` | required; with `APP_ENV=dev`: `dev/keys/jwt.pem` / `dev/keys/jwt.pub.pem` | ES256 key pair. Never deploy the development keys: anyone can sign tokens with them |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | `text` is easier to read locally |
| `PUBSUB` | `nats` | `nats` (works across instances) or `local` (in-process, single instance only) |
| `NATS_URL` | `nats://localhost:4222` | NATS server(s), comma-separated for a cluster |
| `IMPORT_WORKERS` | `2` | GPX imports processed at the same time per instance (bounds memory) |
| `UPLOAD_SLOTS` | `4` | Uploads read into memory at the same time; more wait, then get 503 |
| `IMPORT_JOB_RETENTION` | `168h` | Finished import jobs are deleted after this |
| `IMPORT_SHUTDOWN_GRACE` | `15s` | How long running imports may finish on shutdown before going back to the queue |
| `SHUTDOWN_TIMEOUT` | `25s` | Overall graceful shutdown limit |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `20` / `40` | All API requests, per user (or per IP when anonymous). `0` disables |
| `RATE_LIMIT_AUTH_PER_MINUTE` / `RATE_LIMIT_AUTH_BURST` | `20` / `10` | Login, registration and token refresh, per IP |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | Enables OpenTelemetry tracing over OTLP/HTTP (e.g. `http://localhost:4318`); other `OTEL_*` variables apply too |

## Authentication

No cookies are used.

- `POST /auth/register` or `/auth/login` returns `{access_token, expires_in, refresh_token, refresh_expires_in, user}`.
- Send `Authorization: Bearer <access_token>` with every request (valid 15 minutes).
- When it expires, `POST /auth/refresh` with `{"refresh_token": "..."}` returns a new session. The refresh token (valid 30 days) is rotated on every use, so always keep the newest one.
- The web UI keeps the access token in memory and the refresh token in localStorage.
- Reusing an old refresh token ends that whole session (theft detection).
- Changing your password or "log out everywhere" also invalidates existing access tokens immediately.
- The event stream ends when its access token expires, so revoked sessions don't linger.

| Method | Path | Description |
|---|---|---|
| POST | `/auth/register` | `{email, password (8–72 bytes), display_name?}` → 201 |
| POST | `/auth/login` | `{email, password}` |
| POST | `/auth/refresh` | `{refresh_token}`; returns a new session |
| POST | `/auth/logout` | `{refresh_token}`; ends that session |
| POST | `/auth/logout-all` | Ends all sessions |
| GET / PATCH | `/auth/me` | Profile; PATCH `{display_name?, email?, current_password}` (password needed to change email) |
| PUT | `/auth/me/password` | `{current_password, new_password}`; returns a new session |
| DELETE | `/auth/me` | `{password}`; deletes the account and everything it owns |

## Permissions

| | Owner | Editor (shared) | Viewer (shared, public, or via collection) |
|---|---|---|---|
| View route, geometry, history | ✓ | ✓ | ✓ |
| Edit name/description/geometry, restore versions | ✓ | ✓ | |
| Change visibility, share, delete | ✓ | | |

- Routes and collections are private by default. Others get **404** for things they can't see, and **403** for things they can see but not change.
- Collections have the same roles. Collection editors can rename it and add or remove routes; only the owner can change visibility, share or delete it.
- You can add your own routes or public routes to a collection. Adding your own route lets everyone who can see the collection see that route too.
- Trips are private: only their owner sees them (everyone else gets 404). You can log a trip on any route you can see.

## API

All list endpoints return `{items, page, page_size, total, total_pages}`. Errors are `{"error": "..."}`.

### Routes and collections

| Method | Path | Description |
|---|---|---|
| POST | `/routes` | Synchronous upload for small files and scripts (`multipart/form-data`: `file`, optional `name`, `visibility`). 201, 413 (>20 MB), 422 (invalid GPX). The web UI uses background imports instead |
| GET | `/routes` | List. `scope=mine\|shared\|public\|all` (default `mine`, or `public` when logged out), `collection_id`, `page`, `page_size` (≤100), `name`, `min_distance_m`, `max_distance_m`, `min_ascent_m`, `max_ascent_m`, `bbox=minLon,minLat,maxLon,maxLat`, `sort=created_at\|updated_at\|name\|distance\|ascent`, `order=asc\|desc` |
| GET | `/routes/{id}` | Metadata, stats, `version`, `my_role`. `ETag` = version |
| PATCH | `/routes/{id}` | `{name?, description?, visibility?}`. Name/description changes create a new version |
| PUT | `/routes/{id}/geometry` | New geometry as a GPX upload (multipart) or GeoJSON LineString/MultiLineString (`application/geo+json`); creates a new version |
| DELETE | `/routes/{id}` | Owner only; 204 |
| GET | `/routes/{id}/geometry` | GeoJSON Feature (MultiLineString, with elevation when available) |
| GET | `/routes/{id}/elevation` | Elevation profile (at most 1000 points) |
| GET | `/routes/{id}/versions` | Version history, newest first |
| GET | `/routes/{id}/versions/{n}/geometry` | Geometry of an old version |
| POST | `/routes/{id}/versions/{n}/restore` | Saves version n as a new version |
| GET / PUT | `/routes/{id}/shares` | List shares; PUT `{email, role: viewer\|editor}` adds or changes one |
| DELETE | `/routes/{id}/shares/{user_id}` | Owner removes a share, or users remove themselves |
| POST | `/collections` | `{name, description?, visibility?}` |
| GET | `/collections` | `scope=mine\|shared\|editable\|public\|all`, `route_id`, `page`, `page_size` |
| GET / PATCH / DELETE | `/collections/{id}` | Deleting a collection keeps its routes |
| PUT / DELETE | `/collections/{id}/routes/{route_id}` | Add / remove a route |
| GET / PUT / DELETE | `/collections/{id}/shares[/{user_id}]` | Same as route shares |
| GET | `/audit-events` | Your actions and events on things you own, newest first. `limit` (≤100), `before_id` (from `next_before_id`), `subject_id` |
| GET | `/healthz` | 200 when the database is reachable |

**Concurrent edits:** send `If-Match: "<version>"` with PATCH, PUT geometry, restore and new-version imports. If someone else changed the route since, you get **412** instead of overwriting their change.

**Transactions:** every change is written in one database transaction together with its version snapshot and audit event, so either all of it is stored or none of it.

Ascent/descent ignore elevation changes smaller than 3 m (GPS noise); distance is not counted across gaps between track segments. Missing elevations are interpolated from neighbouring points.

### Background imports

GPX files up to **50 MB** are uploaded once and processed by a pool of workers: parsing, computing statistics and saving the route happen in the background while progress is reported live.

| Method | Path | Description |
|---|---|---|
| POST | `/import-jobs` | `multipart/form-data`: `file`, optional `name`, `visibility`; or `route_id` to import as a new version of that route (with `If-Match`). Send an `Idempotency-Key` header. **202** with the job; **200** with the existing job (and `Idempotent-Replayed: true`) when the key was already used for the same upload; 422 if it was used for a different one; 413 over 50 MB; 429 with 10 imports already in progress; 503 when the server is busy reading other uploads |
| GET | `/import-jobs` | Your jobs, newest first. `status=active\|finished\|all`, `page`, `page_size` |
| GET | `/import-jobs/{id}` | One job |
| POST | `/import-jobs/{id}/cancel` | Cancels a queued job immediately, or stops a running one (it ends as `canceled`, without creating a route). 409 when finished |
| POST | `/import-jobs/{id}/retry` | Queues a `failed` or `canceled` job again with fresh attempts |
| DELETE | `/import-jobs/{id}` | Removes a finished job (409 while queued or running) |

A job has `status` (`queued` → `running` → `succeeded` \| `failed` \| `canceled`), `stage` (`queued`, `starting`, `parsing`, `analyzing`, `saving`, `done`), `progress` (0–100), `attempts`/`max_attempts`, `error`, `route_id` (the created or updated route) and `revision`, which grows with every change so clients can ignore stale updates.

How it works:

- **Queue in PostgreSQL.** Workers claim jobs with `FOR UPDATE SKIP LOCKED`, so several workers and instances never take the same job. New jobs wake idle workers on every instance; they also poll every 2 s.
- **Leases.** A running job is leased to its worker, which renews the lease while working. If an instance crashes, its jobs are queued again when the lease expires (and fail after the last attempt).
- **Retries.** Invalid files, missing routes, lost permissions or a changed route version fail immediately. Other errors (e.g. the database being unavailable) are retried up to 3 times with exponential backoff (2 s, 4 s, …). Internal error details are logged, not shown to users.
- **Exactly one result.** The route and the job's `succeeded` status are written in one transaction, which also checks that the job wasn't canceled meanwhile.
- **Bounded resources.** At most `IMPORT_WORKERS` files are parsed at once per instance and `UPLOAD_SLOTS` uploads read at once; each user may have 10 imports in progress. The file is kept until the import succeeds (so failed jobs can be retried) and deleted with the job after `IMPORT_JOB_RETENTION`.
- **Shutdown.** On SIGTERM, workers stop taking jobs; running ones may finish within `IMPORT_SHUTDOWN_GRACE`, the rest go back to the queue without using up an attempt.

### Live events

`GET /events` (with the `Authorization` header) is a [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html) stream of your own notifications. Browsers read it with `fetch()`, since `EventSource` can't send headers.

- `event: ready` — connected. Reload anything that might have changed while you were disconnected.
- data events: `{"type":"job","job":{…}}` on every import job change.
- `event: reauth` — your access token expired: refresh it and reconnect. `event: resync` — events may have been missed: reload and reconnect. `event: shutdown` — the server is stopping: reconnect (to another instance).

### Trips

A trip is a route you went on, with the date and how long it took: your personal log. When the route's GPX file has timestamps, the date and duration default to the recorded ones (routes report the recorded `duration_s`). A trip keeps the route's `distance_m` and `ascent_m` from when it was logged; if the route is later deleted or hidden from you, the trip stays with `route_id`/`route_name` null.

| Method | Path | Description |
|---|---|---|
| POST | `/trips` | Log a trip on a route you can see: `{route_id, date? (YYYY-MM-DD), duration_s?, name? (default: the route's), notes?}`. Or upload the track with it as multipart/form-data: `file` plus optional `name`, `notes`, `date`, `duration_s` and `visibility` (of the new route, private by default); the file becomes a new route of yours. 400 when no date or duration is given and the file has none. 201 |
| GET | `/trips` | Your trips, newest date first. `year`, `route_id`, `page`, `page_size` |
| GET | `/trips/stats` | `{trips, duration_s, distance_m, ascent_m, first_date, last_date}` over your trips; same `year`/`route_id` filters |
| GET / PATCH / DELETE | `/trips/{id}` | PATCH `{name?, notes?, date?, duration_s?}`; the route can't be changed. Deleting a trip keeps its route |

Durations are 1 second to 60 days; dates may not be in the future.

## Operations

- **Logs** are structured (`log/slog`, JSON by default) with request IDs, user IDs, trace IDs and durations in milliseconds; import jobs log their start and outcome with job ID, attempt and duration.
- **Metrics** (Prometheus, `METRICS_ADDR`): request counts and latencies per route pattern, requests in flight, rate-limited requests, import jobs by outcome and duration, queue length, running jobs, upload sizes and pub/sub events.
- **Tracing** (OpenTelemetry, when `OTEL_EXPORTER_OTLP_ENDPOINT` is set): a span per request (continuing incoming `traceparent` headers) and per import job, with parse, analyze and save steps.
- **Rate limits** answer **429** with `Retry-After`.
- **Graceful shutdown** (SIGTERM/Ctrl+C): stop accepting connections, tell event streams to reconnect elsewhere, finish in-flight requests, and let import jobs finish or requeue them.
- **Several instances** can run against the same database: the job queue and leases work across them, and events go through the shared NATS server.

## Sample data

`make seed` (or `scripts/dev.sh --seed`) fills a running API with 22 cycle-touring routes from [gps-touring/sample-gpx](https://github.com/gps-touring/sample-gpx), owned by `demo@example.com` / `demo-password` and grouped into two collections. The files are downloaded at seed time from a pinned commit (the repository has no license, so they are not copied here). Running it again only adds what is missing. Options: `go run ./cmd/seed -h` (other API URL, account, private visibility).

## Tests

```sh
go test ./...   # unit tests
TEST_DATABASE_URL='postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable' go test ./...   # + end-to-end API tests (apitest/) against PostGIS
cd frontend && npm run lint && npx tsc --noEmit && npm test && npm run build
cd frontend && npm run e2e   # browser smoke test against a real API (needs the db container)
```

The API tests delete all data in the test database. They cover background imports (progress, idempotency, cancel, retries, crashed workers, shutdown, large files), trips (defaults from the recording, uploads, privacy, stats), rate limits and CORS, and golden files in `apitest/testdata/` pin every endpoint and response shape (`make contract-update` after an intended API change). CI (`.github/workflows/ci.yml`) runs gofmt, `go vet`, all Go tests with `-race` against a PostGIS service, lints, type-checks, unit-tests and builds the web UI, runs the browser smoke test, and builds both Docker images.
