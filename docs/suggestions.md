# Suggestions: what to add and what to improve

Ideas for where Travel Docs could go next, based on the current code. Each
item says **what**, **why**, and a rough **size**: S is a day or less, M a
few days, L a week or more. Start with section 1 (high value, low effort),
then pick from the rest by interest.

## 1. Quick wins (start here)

| # | Suggestion | Why | Size |
|---|---|---|---|
| 1 | **GPX download** (`GET /routes/{id}/gpx`) and a "Download" button | Routes go in but can't come out as GPX; every other app expects GPX. The geometry and elevation are already stored. | S |
| 2 | **Gzip responses** (Echo `middleware.Gzip`) | GeoJSON geometries are large and compress about 10×. A one-line change with a big speedup on slow connections. | S |
| 3 | **Estimated time on routes** (Naismith's rule: 5 km/h plus 1 h per 600 m of ascent) | Answers "how long will this take?" before you go, alongside the recorded time. | S |
| 4 | **Personal pace from your trips** | You already log time and distance; use your own average to estimate routes ("about 2 h 40 min for you"). | S–M |
| 5 | **Elevation chart ↔ map hover** | Hovering the chart shows a dot at that point on the map. Expected in every hiking app, and the data is there. | S–M |
| 6 | **Update `PLAN.md`** | Stage 4 still describes live group trips, which were replaced by the trip log. | S |
| 7 | **Moving time vs elapsed time** | GPX timestamps let you exclude stops (speed below about 0.5 m/s). Trips could show both. | S |
| 8 | **Accessibility checks in e2e** (`@axe-core/playwright`) | Catches contrast and labelling regressions automatically, which matters after the palette changes. | S |

## 2. Features worth building

### Trips (the newest and most personal part)

- **Record the actual track, not just the route.** Today a trip points at a planned route. Let a trip optionally carry its *own* recorded GPX, so you can compare planned and actual on the map. (M)
- **Photos on trips.** Upload a few photos with optional GPS from EXIF and place them on the map. Needs object storage; see section 3. (L)
- **Stats page:** per-month bar chart, a yearly heatmap calendar (GitHub-style), longest trip, biggest climb, streaks. Everything comes from `trips` with SQL aggregates. (M)
- **Goals:** "100 km this year" with a progress ring on the Trips page. (S–M)
- **Weather on the day,** fetched once when logging from a free historical weather API using the route's start point and date. (M)
- **Companions:** tag other users (or just names) on a trip, without bringing back the complexity of live group trips. (S–M)
- **Export:** CSV of all trips, and a full account export (GPX plus JSON) for data portability. (S–M)
- **Import from Strava, Garmin or Komoot** via their export files (bulk GPX/FIT), turning each activity into a route plus a trip in one go. Reuse the background import queue. (M–L)

### Routes

- **Waypoints and points of interest** (`<wpt>` in GPX): springs, huts, viewpoints, shown on the map and in the elevation chart. (M)
- **Tags and difficulty** (e.g. T1–T6 / easy–hard) with filters. (S–M)
- **Search by place name** (geocoding: "routes near Zermatt") on top of the existing bounding-box filter. (M)
- **Draw or edit a route on the map,** snapping to trails with a routing service (e.g. OpenRouteService or BRouter). (L)
- **All routes as one map layer:** serve vector tiles from PostGIS (`ST_AsMVT`) so the explorer can show every visible route at once instead of one at a time. (M)
- **Public profile pages** (`/u/alex`) listing public routes and collections. (M)

### Collections

- **Ordering and multi-day itineraries:** "day 1, day 2…" with per-day totals. (M)
- **Cover image and description formatting** (Markdown). (S–M)

## 3. Improvements to the existing system

### Security and accounts

- **Password reset and email verification.** Currently a forgotten password means a lost account. Needs an email sender (SMTP or a provider) and single-use tokens; the refresh-token code is a good model. (M)
- **Refresh token in an httpOnly cookie** instead of localStorage, so an XSS bug can't steal sessions. Needs CSRF protection (SameSite=strict plus a header check). This changes the API contract, so plan it. (M)
- **Tighter Content-Security-Policy:** replace `'unsafe-inline'` scripts with nonces. The Next.js docs in `node_modules/next/dist/docs` describe how. (S–M)
- **Trusted proxy configuration for rate limits:** per-IP limits rely on `RealIP()`. Behind a load balancer, configure which proxies to trust, or clients can spoof `X-Forwarded-For`. (S)

### Storage and performance

- **Move uploaded files out of PostgreSQL.** Import payloads (up to 50 MB) live in `import_jobs.payload`. Object storage (S3, or MinIO locally) keeps the database small and backups fast; store only the key. Photos (above) would need this too. (M)
- **One upload path:** `POST /routes` (direct, 20 MB) and `/import-jobs` (background, 50 MB) now share validation, but are still two mechanisms. Consider making direct upload a thin wrapper that enqueues and waits. (M)
- **Simplified geometry for previews** (`ST_Simplify` or precomputed low-detail versions) when showing many routes or zoomed-out maps. (S–M)
- **Keyset pagination** (`WHERE (created_at, id) < (…)`) instead of OFFSET for large lists; OFFSET gets slow and skips items when data changes. (M)
- **Audit log retention:** `audit_events` grows forever; add a retention job like the one for import jobs. (S)

### Maps

- **Switch from Leaflet raster tiles to MapLibre GL vector tiles.** Map styles could then use the app's palette directly instead of CSS filters on images, dark mode would be a proper style, and route colors could follow the theme tokens (today they're hard-coded hex in `Map.tsx`). It also gives smooth zoom and 3D terrain. (L)
- **Use a tile provider** (MapTiler, Stadia, or self-hosted). Direct use of `tile.openstreetmap.org` is allowed only for light traffic under OSM's tile policy. (S)

### API and code quality

- **OpenAPI spec plus generated TypeScript types.** The contract golden files keep the API stable, but the frontend types in `src/lib/types.ts` are still hand-written. Generating them removes a class of bugs. (M)
- **Machine-readable error codes:** add `code` (e.g. `route_version_conflict`) next to `error` messages, so the UI can react without matching text and messages can be translated. (S–M)
- **Data fetching library in the frontend** (TanStack Query): caching, deduplication, retries and background refresh, replacing the many hand-written `useEffect` + `active` flag patterns. (M)
- **Component tests** (Testing Library) for the form dialogs, and visual regression snapshots (Playwright screenshots) so theme changes are reviewed deliberately. (M)
- **API versioning** (`/v1/…`) before any external client depends on the API. (S)

### Operations

- **Production compose profile or deployment guide:** TLS reverse proxy, real JWT keys, backups (`pg_dump` schedule), NATS with auth. (M)
- **Grafana dashboard plus alerts** for the metrics that already exist: import failures, queue depth, 5xx rate, latency. (S–M)
- **Down migrations or a migration tool** (e.g. goose, Atlas) once there's real data, so a bad migration can be rolled back. (S–M)
- **Seed script with realistic demo data** (several routes, collections and a year of trips), so new contributors and screenshots look real. (S)

## 4. Bigger bets

- **Mobile-first PWA:** offline route maps (cached tiles plus geometry), "start trip" recording with the phone's GPS, then sync, so the app becomes something you use *on* the trail, not just before and after it. (L)
- **Social layer:** follow friends, see their public trips in a feed, react or comment. Adds moderation and privacy work; do it only if sharing is the goal. (L)
- **Trip planning assistant:** suggest routes from your collections that match a target time or difficulty, with weather and daylight for a chosen date. (L)

## 5. A suggested order

1. GPX download, gzip, estimated time and personal pace (section 1): visible value in a few days.
2. Password reset (section 3): a real gap for any user beyond yourself.
3. Trip stats page and goals (section 2): makes the trip log rewarding.
4. Object storage, then photos on trips.
5. MapLibre vector maps: unlocks the theming and "all routes on the map" ideas.
6. PWA and offline: the big step toward on-trail use.
