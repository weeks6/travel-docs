Stage 3 — Asynchronous processing
Weeks 6–8
- Move GPX analysis into background workers.
- Report progress to the frontend.
- Support cancellation, retries, and failed jobs.
- Prevent duplicate imports with idempotency.
- Handle large files with bounded concurrency.
- Add structured logs and graceful shutdown.
  
  Backend skills: goroutines, context, worker pools, synchronization, reliability.

Stage 4 — Real-time group trips
Weeks 9–12+
- Trip invitations and participant management.
- GPS location updates over WebSockets.
- Presence and last-known positions.
- Trip history and location retention policies.
- Rate limiting, metrics, tracing, and load tests.