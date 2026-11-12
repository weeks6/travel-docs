# Route scraper

A separate service that finds publicly available routes, validates them and
exports them into Travel Docs. It's its own Go module, process and container
with its own state file. It only talks to the app through the app's public
HTTP API, as an ordinary user account.

```
region by region:
  official datasets (NPS, USFS, DOC, Parks Canada) ─┐
  OpenStreetMap (Overpass API) ──────────────────────┴─► validate ─► duplicate? ─► changed? ─► Travel Docs API
                                                                    (state file)               POST /import-jobs
```

## Sources

| Source | Covers | License | Notes |
|---|---|---|---|
| `osm`: [OpenStreetMap](https://www.openstreetmap.org) via Overpass | the whole world | ODbL, attribution | named hiking and cycling route relations |
| `nps`: [U.S. National Park Service public trails](https://mapservices.nps.gov/arcgis/rest/services/NationalDatasets/NPS_Public_Trails/FeatureServer/0) | U.S. national parks | public domain (U.S. federal work) | segments joined by park and trail name |
| `usfs`: [USDA Forest Service National Forest System trails](https://apps.fs.usda.gov/arcx/rest/services/EDW/EDW_TrailNFSPublish_01/MapServer/0) | U.S. national forests | CC BY 4.0 | land trails only; segments joined by trail ID |
| `doc-nz`: [New Zealand Department of Conservation tracks](https://services1.arcgis.com/3JjYDyG3oajxU6HO/ArcGIS/rest/services/DOC_Tracks_Open_Data/FeatureServer/0) | New Zealand | CC BY 4.0, Crown copyright | open tracks only |
| `parks-canada`: [Parks Canada trails](https://open.canada.ca/data/en/dataset/64a90e8d-5bc0-4027-8645-b5881b4068d4) | Canada's national parks and sites | Open Government Licence – Canada | official trails; segments joined by place and name |

Every exported route's description names its source, links to it, and carries the attribution its license requires. Elsewhere (South America, Africa, Asia, Europe) OpenStreetMap is the source: it is the one open, worldwide trail dataset. Sites like Strava, Komoot, AllTrails or Wikiloc forbid scraping in their terms, so they are not sources.

The official datasets are ArcGIS services; adding another is a few lines in `datasets.go` (layer URL, filter, which fields identify a trail, how to name it, the attribution).

## Regions

`-regions world` (the default) is about 45 well-known hiking and cycling areas on every continent: Yosemite, Banff, Torres del Paine, the Annapurna and Khumbu areas, Kilimanjaro, the Drakensberg, the Japanese Alps, Fiordland and more (`regions.go`; `go run . -h` lists them). Use a continent (`europe`, `north-america`, `south-america`, `africa`, `asia`, `oceania`), region names, or your own boxes (`south,west,north,east`), separated by `;`.

Regions are small (under about a degree a side) because Overpass queries over large, route-dense areas time out. Runs take turns: each scans the next `-regions-per-run` regions (6 by default), with continents interleaved, and the state file remembers where to continue. With the defaults and a daily run, the whole world comes round about once a week. A run that reaches `-limit` stops inside a region; the next run resumes there.

## What it does

1. **Discover:** asks each source for the trails in the region: OpenStreetMap's hiking and cycling route relations (named, signposted routes; `osm.go`), and the official datasets that cover it (`arcgis.go`, `datasets.go`). A trail crossing the region's edge is fetched whole. Pieces are joined into continuous lines: OSM relations in their order, skipping "alternative" and "excursion" branches; dataset segments by matching their ends.
2. **Validate:** skips data that isn't a usable route (`validate.go`):
   - no name, or marked as needing fixes in OSM (a `fixme` tag or "fixme" in the name);
   - invalid coordinates, or jumps over 2 km between consecutive points;
   - fewer than 10 points, shorter than 1 km or longer than 1,000 km;
   - more than 20 separate pieces, or under half its length in the main piece (a broken relation).

   The app validates the GPX again when importing it.
3. **Skip duplicates:** official datasets are read before OpenStreetMap. A trail whose start and end are within 150 m of a route already exported from another source (either direction), and whose length is within 15%, is skipped, so a park trail isn't imported twice.
4. **Skip unchanged:** a fingerprint of the geometry is stored per route in `data/state.json` (`state.go`). Unchanged routes are not sent again.
5. **Export** (`export.go`):
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
go run . -regions "yosemite;asia;45.8,6.8,46.0,7.1" -regions-per-run 0 -limit 20
go run . -sources nps,usfs -regions north-america
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
| `-regions` | `SCRAPER_REGIONS` | `world` | Region names, continents, `world` or `south,west,north,east`, separated by `;` |
| `-regions-per-run` | `SCRAPER_REGIONS_PER_RUN` | `6` | Regions per run, continuing where the last run stopped (`0`: all) |
| `-sources` | `SCRAPER_SOURCES` | all | `osm`, `nps`, `usfs`, `doc-nz`, `parks-canada`, separated by `,` |
| `-routes` | `SCRAPER_ROUTES` | `hiking,bicycle` | OSM `route=*` types (also `mtb`, `foot`, …) |
| `-interval` | `SCRAPER_INTERVAL` | `0` (run once) | e.g. `24h` |
| `-limit` | `SCRAPER_LIMIT` | `0` (no limit) | Exports per run |
| `-state` | `SCRAPER_STATE` | `data/state.json` | What was exported |
| `-overpass` | `SCRAPER_OVERPASS` | public overpass-api.de | Overpass endpoint |
| `-user-agent` | `SCRAPER_USER_AGENT` | `route-scraper/0.1` | Add contact details for regular use |
| `-dry-run`, `-min-length-m`, `-max-length-m` | | | |

## Being a good citizen

- **Why OpenStreetMap:** OSM data is open (ODbL) and may be reused with attribution, which the scraper adds to every route. Most route sites (Strava, Komoot, AllTrails, …) forbid scraping in their terms, so they are deliberately not sources.
- **Official datasets** are queried region by region (never downloaded whole: the Forest Service layer alone is hundreds of megabytes), with backoff when a server is busy. A source that fails is logged and skipped; the region comes round again.
- **The public Overpass server is shared and meant for light use.** When it's busy, it answers 502/504 and asks clients to back off; the scraper retries with exponential backoff and gives up after a few attempts. For scheduled or large-area scraping, run your own Overpass instance (or use a commercial one) and point `-overpass` at it. Keep regions small, and set `-user-agent` to something that identifies you.

## Adding a source

For an ArcGIS trail layer, add an `ArcGISDataset` to `datasets.go`. Otherwise implement `Source` (`track.go`): for a region, return `Track`s with a stable `SourceID` (prefixed with the source name), a name, a source URL, the attribution the source's license requires, and line segments; and add it to `buildSources` (`main.go`). Only use sources whose terms allow automated access and reuse, and check the license yourself.

## Tests

`make scraper-test`: validation rules, region parsing, way and segment joining (with a real Overpass response in `testdata/`), the ArcGIS source against a fake server (two-step queries, paging, quoting), duplicate detection, GPX output, the state file, and full runs against fake sources and a fake Travel Docs API. The full runs cover registering, exporting, skipping unchanged routes, a changed geometry becoming a new version, waiting out 429s, dry runs, regions taking turns, resuming after the export limit, and an official trail winning over OSM's copy.
