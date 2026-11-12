import { defineConfig, devices } from "@playwright/test";

// End-to-end smoke test of the web UI against a real API (`npm run e2e`).
// Starts the Go API (in-process event broker, development keys) and a
// production build of the web UI on their own ports. Needs PostgreSQL: by
// default the Docker test database (`docker compose up -d db`); set
// E2E_DATABASE_URL to use another. Test users get unique emails, so
// existing data is untouched.

const API_PORT = 18081;
const WEB_PORT = 3100;
const API_URL = `http://localhost:${API_PORT}`;
const WEB_URL = `http://localhost:${WEB_PORT}`;
const DATABASE_URL =
  process.env.E2E_DATABASE_URL || "postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable";

export default defineConfig({
  testDir: "./e2e",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: { baseURL: WEB_URL, trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: "go run .",
      cwd: "..",
      url: `${API_URL}/healthz`,
      timeout: 180_000,
      env: {
        APP_ENV: "dev",
        PUBSUB: "local",
        ADDR: `:${API_PORT}`,
        METRICS_ADDR: "off",
        CORS_ORIGINS: WEB_URL,
        DATABASE_URL,
        LOG_FORMAT: "text",
        LOG_LEVEL: "warn",
      },
      stdout: "pipe",
    },
    {
      // A production build in its own directory, so it doesn't disturb a
      // running `next dev` (only one is allowed per project) or .next.
      // output: "standalone" is served by its own server.js, with the static
      // files copied next to it (see the Next.js docs on `output`).
      command:
        "npx next build && cp -R .next-e2e/static .next-e2e/standalone/.next-e2e/ && node .next-e2e/standalone/server.js",
      url: WEB_URL,
      timeout: 300_000,
      env: {
        NEXT_PUBLIC_API_URL: API_URL,
        NEXT_DIST_DIR: ".next-e2e",
        NEXT_TELEMETRY_DISABLED: "1",
        PORT: String(WEB_PORT),
      },
    },
  ],
});
