Stage 1 — A useful personal tool
Weeks 1–2
- Upload and validate GPX files.
- Store route metadata and geometry in PostgreSQL.
- Display the route on a map.
- Calculate distance, ascent, descent, and elevation profile.
- Add pagination, filtering, and deletion.
- Write unit and integration tests.

## Running

```sh
docker compose up -d db   # PostgreSQL 17 + PostGIS (also creates travel_docs_test)
go run .                  # http://localhost:8080 — map UI; migrations run on startup
```

Config via env: `DATABASE_URL` (default `postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable`), `ADDR` (default `:8080`).

## API

| Method | Path | Description |
|---|---|---|
| POST | `/routes` | Upload GPX (`multipart/form-data`: `file`, optional `name`). 201, 413 (>20 MB), 422 (invalid GPX) |
| GET | `/routes` | List. Query: `page`, `page_size` (≤100), `name`, `min_distance_m`, `max_distance_m`, `min_ascent_m`, `max_ascent_m`, `bbox=minLon,minLat,maxLon,maxLat`, `sort=created_at\|name\|distance\|ascent`, `order=asc\|desc` |
| GET | `/routes/{id}` | Route metadata and stats |
| GET | `/routes/{id}/geometry` | GeoJSON Feature (MultiLineString) |
| GET | `/routes/{id}/elevation` | Elevation profile (`distance_m`, `elevation_m`; at most 1000 points) |
| DELETE | `/routes/{id}` | 204, or 404 |

Ascent/descent ignore elevation changes smaller than 3 m (GPS noise); distance is not counted across gaps between track segments.

## Tests

```sh
go test ./...   # unit + handler tests
TEST_DATABASE_URL='postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable' go test ./...   # + PostGIS integration tests
```
