## Running

Quickest way during development:

```sh
scripts/dev.sh --seed --open
```

It starts Docker and the database if needed, runs the app with live reload (when [air](https://github.com/air-verse/air) is installed), creates a demo account (`demo@example.com` / `demo-password`) with a sample route, and opens http://localhost:8080. Ctrl+C stops the app. See `scripts/dev.sh --help` for options.

Locally by hand (Go installed):

```sh
docker compose up -d db   # PostgreSQL 17 + PostGIS (also creates travel_docs_test)
go run .                  # http://localhost:8080 — map UI; migrations run on startup
```

Everything in Docker:

```sh
docker compose up --build   # app + database
docker compose watch        # same, and rebuilds the app when files change
```

Config via env:

| Variable | Default |
|---|---|
| `DATABASE_URL` | `postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable` |
| `ADDR` | `:8080` |
| `JWT_PRIVATE_KEY_FILE` / `JWT_PUBLIC_KEY_FILE` | `test_key.pem` / `test_key.pub.pem` (ES256, development only) |

## Authentication

No cookies are used.

- `POST /auth/register` or `/auth/login` returns `{access_token, expires_in, refresh_token, refresh_expires_in, user}`.
- Send `Authorization: Bearer <access_token>` with every request (valid 15 minutes).
- When it expires, `POST /auth/refresh` with `{"refresh_token": "..."}` returns a new session. The refresh token (valid 30 days) is rotated on every use, so always keep the newest one.
- The browser UI keeps the access token in memory and the refresh token in localStorage.
- Reusing an old refresh token ends that whole session (theft detection).
- Changing your password or "log out everywhere" also invalidates existing access tokens immediately.

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

## API

All list endpoints return `{items, page, page_size, total, total_pages}`.

| Method | Path | Description |
|---|---|---|
| POST | `/routes` | Upload GPX (`multipart/form-data`: `file`, optional `name`, `visibility`). 201, 413 (>20 MB), 422 (invalid GPX) |
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

**Concurrent edits:** send `If-Match: "<version>"` with PATCH, PUT geometry and restore. If someone else changed the route since, you get **412** instead of overwriting their change.

**Transactions:** every change is written in one database transaction together with its version snapshot and audit event, so either all of it is stored or none of it.

Ascent/descent ignore elevation changes smaller than 3 m (GPS noise); distance is not counted across gaps between track segments. Missing elevations are interpolated from neighbouring points.

## Tests

```sh
go test ./...   # unit tests
TEST_DATABASE_URL='postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable' go test ./...   # + end-to-end API tests (apitest/) against PostGIS
```

The API tests delete all data in the test database. CI (`.github/workflows/ci.yml`) runs gofmt, `go vet`, all tests with `-race` against a PostGIS service, and builds the Docker image.
