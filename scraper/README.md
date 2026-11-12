# Route scraper

A separate service that finds publicly available routes, validates them and
exports them into Travel Docs. It's its own Go module, process and container
with its own state file. It only talks to the app through the app's public
HTTP API, as an ordinary user account.

```
OpenStreetMap (Overpass API) ──► discover ──► validate ──► changed? ──► Travel Docs API
                                                           (state file)   POST /import-jobs
```

## What it does

1. **Discover:** asks OpenStreetMap, through the Overpass API, for hiking and cycling route relations (named, signposted routes) inside the configured regions (`osm.go`). It joins each relation's ways into continuous lines, skipping "alternative" and "excursion" branches.
2. **Validate:** skips data that isn't a usable route (`validate.go`):
   - no name, or marked as needing fixes in OSM (a `fixme` tag or "fixme" in the name);
   - invalid coordinates, or jumps over 2 km between consecutive points;
   - fewer than 10 points, shorter than 1 km or longer than 1,000 km;
   - more than 20 separate pieces, or under half its length in the main piece (a broken relation).

   The app validates the GPX again when importing it.
3. **Skip unchanged:** a fingerprint of the geometry is stored per route in `data/state.json` (`state.go`). Unchanged routes are not sent again.
4. **Export** (`export.go`):
   - New routes are uploaded as GPX background imports. The description then gets the source link and the **attribution OSM's license requires**.
   - Routes whose geometry changed in OSM are uploaded as a **new version** of the route created before, so the app keeps the history.
   - Uploads carry an `Idempotency-Key`, so a retried upload can't create a duplicate.
   - Waits whenever the app answers 429 (rate limit, or 10 imports in progress).
   - Logs in again when the access token expires.
   - Re-imports routes that were deleted in the app.

The state is saved after every export, so an interrupted run never repeats work.

## Running it

Against a running API (e.g. `scripts/dev.sh`):

```sh
make scrape                          # one run with the defaults
cd scraper && go run . -dry-run      # discover and validate only; prints what it would export
go run . -regions "46.45,7.75,46.7,8.1;45.8,6.8,46.0,7.1" -routes hiking,bicycle,mtb -limit 20
go run . -interval 24h               # keep running, once a day
go run . -h                          # all options
```

With Docker, as a daily job next to the app:

```sh
docker compose --profile scraper up -d
```

Configuration, as flags or environment variables:

| Flag | Variable | Default | |
|---|---|---|---|
| `-api` | `SCRAPER_API` | `http://localhost:8080` | Travel Docs API |
| `-email`, `-password` | `SCRAPER_EMAIL`, `SCRAPER_PASSWORD` | `scraper@example.com` / `scraper-password` | Account that owns the routes; registered on first run. Use real credentials anywhere but locally. |
| `-visibility` | `SCRAPER_VISIBILITY` | `public` | Of new routes |
| `-regions` | `SCRAPER_REGIONS` | Lauterbrunnen / Grindelwald | `south,west,north,east`, several separated by `;` |
| `-routes` | `SCRAPER_ROUTES` | `hiking,bicycle` | OSM `route=*` types (also `mtb`, `foot`, …) |
| `-interval` | `SCRAPER_INTERVAL` | `0` (run once) | e.g. `24h` |
| `-limit` | `SCRAPER_LIMIT` | `0` (no limit) | Exports per run |
| `-state` | `SCRAPER_STATE` | `data/state.json` | What was exported |
| `-overpass` | `SCRAPER_OVERPASS` | public overpass-api.de | Overpass endpoint |
| `-user-agent` | `SCRAPER_USER_AGENT` | `route-scraper/0.1` | Add contact details for regular use |
| `-dry-run`, `-min-length-m`, `-max-length-m` | | | |

## Being a good citizen

- **Why OpenStreetMap:** OSM data is open (ODbL) and may be reused with attribution, which the scraper adds to every route. Most route sites (Strava, Komoot, AllTrails, …) forbid scraping in their terms, so they are deliberately not sources.
- **The public Overpass server is shared and meant for light use.** When it's busy, it answers 502/504 and asks clients to back off; the scraper retries with exponential backoff and gives up after a few attempts. For scheduled or large-area scraping, run your own Overpass instance (or use a commercial one) and point `-overpass` at it. Keep regions small, and set `-user-agent` to something that identifies you.

## Adding a source

Implement `Source` (`track.go`): return `Track`s with a stable `SourceID`, a name, a source URL, the attribution the source's license requires, and line segments. Add it to the list in `runOnce` (`main.go`). Only use sources whose terms allow automated access and reuse.

## Tests

`make scraper-test`: validation rules, way joining (with a real Overpass response in `testdata/`), GPX output, the state file, and full runs against a fake Overpass server and a fake Travel Docs API. The full runs cover registering, exporting, skipping unchanged routes, a changed geometry becoming a new version, waiting out 429s, and dry runs.
