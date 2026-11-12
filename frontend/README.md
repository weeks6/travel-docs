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
- UI: [shadcn/ui](https://ui.shadcn.com) (Radix primitives, `src/components/ui/`, added with `npx shadcn@latest add <name>`) on Tailwind CSS v4, with lucide icons. The app's own building blocks (dialogs, fields, stats, empty states…) are in `src/components/common.tsx`.
- Look: a field-journal theme defined as CSS variables in `src/app/globals.css`: warm paper surfaces, pine green as the primary color, a "sunset" accent (`variant="sunset"`) for the main action on a screen, Bricolage Grotesque for headings and figures with Inter for text, pill-shaped buttons, and topographic contour lines (`.topo`) as the recurring motif. Dark mode follows the system setting, including a night version of the map tiles.
- API calls go through the typed functions in `src/lib/api/` (`routesApi`, `tripsApi`, …), never hand-built paths.
- Live updates: import progress comes from the API's event stream (`src/lib/events.tsx`).
- Pages: `/` routes and collections on a map, `/imports` background imports, `/trips` your trip log (log a trip from a GPX file or a route, with totals per year) and `/trips/[id]` one trip on the map.
- `Dockerfile` builds a standalone server (`docker build --build-arg NEXT_PUBLIC_API_URL=… .`).
