# Travel Docs web UI

Next.js app for the Travel Docs API (see the [main README](../README.md)).

```sh
npm ci
NEXT_PUBLIC_API_URL=http://localhost:8080 npm run dev   # http://localhost:3000
npm run lint && npx tsc --noEmit && npm run build
```

- `NEXT_PUBLIC_API_URL` (default `http://localhost:8080`) is built into the bundle; the API must allow this app's origin in `CORS_ORIGINS`.
- Everything runs in the browser: the access token is kept in memory, the refresh token in localStorage (`src/lib/api.ts`).
- Live updates: import progress and invitations come from the API's event stream (`src/lib/events.tsx`); trips use a WebSocket per trip (`src/lib/tripSocket.ts`).
- Pages: `/` routes and collections on a map, `/imports` background imports, `/trips` and `/trips/[id]` live group trips.
- `Dockerfile` builds a standalone server (`docker build --build-arg NEXT_PUBLIC_API_URL=… .`).
