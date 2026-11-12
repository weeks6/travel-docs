# Travel Docs web UI

Next.js app for the Travel Docs API (see the [main README](../README.md)).

```sh
npm ci
NEXT_PUBLIC_API_URL=http://localhost:8080 npm run dev   # http://localhost:3000
npm run lint && npx tsc --noEmit && npm test && npm run build
npm run e2e   # Playwright smoke test: starts the API and a production build (needs `docker compose up -d db`)
```

- `NEXT_PUBLIC_API_URL` (default `http://localhost:8080`) is built into the bundle; the API must allow this app's origin in `CORS_ORIGINS`.
- Everything runs in the browser: the access token is kept in memory, the refresh token in localStorage (`src/lib/api/client.ts`).
- API calls go through the typed functions in `src/lib/api/` (`routesApi`, `tripsApi`, …), never hand-built paths.
- Live updates: import progress comes from the API's event stream (`src/lib/events.tsx`).
- Pages: `/` routes and collections on a map, `/imports` background imports, `/trips` your trip log (log a trip from a GPX file or a route, with totals per year) and `/trips/[id]` one trip on the map.
- `Dockerfile` builds a standalone server (`docker build --build-arg NEXT_PUBLIC_API_URL=… .`).
