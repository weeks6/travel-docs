import path from "node:path";
import { expect, test } from "@playwright/test";

// One pass through the main features, as a user would click through them.
// It checks that the pages, the API client, the event stream and the trip
// WebSocket work together, not every detail (the API tests cover those).

const GPX = path.resolve(__dirname, "../../internal/gpx/testdata/track.gpx");

test("sign up, import a GPX file, see the route, run a live trip", async ({ page }) => {
  const email = `smoke-${Date.now()}-${Math.floor(Math.random() * 1e6)}@example.com`;

  // Sign up.
  await page.goto("/");
  await page.getByRole("button", { name: "Sign up", exact: true }).click();
  const signup = page.getByRole("dialog");
  await signup.getByLabel("Email").fill(email);
  await signup.getByLabel("Display name").fill("Smoke Tester");
  await signup.getByLabel("Password").fill("correct horse battery");
  await signup.getByRole("button", { name: /create account/i }).click();
  await expect(page.locator(".who")).toHaveText("Smoke Tester");

  // Import a GPX file in the background; progress arrives over /events.
  await page.locator('input[type="file"][name="file"]').first().setInputFiles(GPX);
  await page.getByPlaceholder("Name (optional)").first().fill("Smoke route");
  await page.getByRole("button", { name: "Upload", exact: true }).first().click();
  await expect(page.getByText("Imported")).toBeVisible();

  // The new route is listed and its details open.
  await expect(page.getByText("Smoke route").first()).toBeVisible();
  await page.getByText("Smoke route").first().click();
  await expect(page.getByRole("heading", { name: "Smoke route" })).toBeVisible();

  // Create a trip on that route and follow it live.
  await page.getByRole("link", { name: "Trips" }).click();
  await page.getByRole("button", { name: "New trip" }).click();
  const tripForm = page.getByRole("dialog");
  await tripForm.locator('input[name="name"]').fill("Smoke trip");
  await tripForm.locator('select[name="route_id"]').selectOption({ label: "Smoke route" });
  await tripForm.getByRole("button", { name: "Create trip" }).click(); // opens the new trip

  await expect(page.getByRole("heading", { name: "Smoke trip" })).toBeVisible();
  await expect(page.locator(".conn")).toHaveText(/Live/);
  await page.getByRole("button", { name: "Start trip" }).click();
  await expect(page.locator(".badge.status-active")).toBeVisible();

  // Share a simulated position: it comes back over the WebSocket.
  await page.locator("select").filter({ hasText: "Not sharing" }).selectOption("simulate");
  await expect(page.locator(".participants")).toContainText("position just now");
});
