import path from "node:path";
import { expect, test } from "@playwright/test";

// One pass through the main features, as a user would click through them.
// It checks that the pages, the API client and the event stream work
// together, not every detail (the API tests cover those).

const GPX = path.resolve(__dirname, "../../internal/gpx/testdata/track.gpx");

test("sign up, import a GPX file, see the route, log trips", async ({ page }) => {
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

  await expect(page.locator(".stat").filter({ hasText: "Recorded time" })).toContainText("2 min");

  // Log a trip on that route: date and time come from the recording.
  await page.getByRole("button", { name: "Log trip" }).click();
  let dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Date")).toHaveValue("2026-06-01");
  await expect(dialog.getByLabel("Minutes")).toHaveValue("2");
  await dialog.getByLabel("Notes").fill("Sunny");
  await dialog.getByRole("button", { name: "Log trip" }).click();

  await expect(page.getByRole("heading", { name: "Smoke route" })).toBeVisible();
  await expect(page.locator(".stat").filter({ hasText: "Time" }).first()).toContainText("2 min");
  await expect(page.getByText("Sunny")).toBeVisible();

  // Correct the time spent.
  await page.getByRole("button", { name: "Edit" }).click();
  dialog = page.getByRole("dialog");
  await dialog.getByLabel("Hours").fill("1");
  await dialog.getByLabel("Minutes").fill("30");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(page.locator(".stat").filter({ hasText: "Time" }).first()).toContainText("1 h 30 min");

  // Log another trip by uploading its GPX file.
  await page.getByRole("link", { name: "Trips", exact: true }).click();
  await page.getByRole("button", { name: "Log a trip" }).click();
  dialog = page.getByRole("dialog");
  await dialog.locator('input[type="file"]').setInputFiles(GPX);
  await dialog.getByLabel("Date").fill("2026-05-01");
  await dialog.getByLabel("Hours").fill("2");
  await dialog.getByLabel("Name").fill("Uploaded trip");
  await dialog.getByRole("button", { name: "Log trip" }).click();
  await expect(page.getByRole("heading", { name: "Uploaded trip" })).toBeVisible();
  await expect(page.locator(".stat").filter({ hasText: "Time" }).first()).toContainText("2 h");

  // The log lists both, newest first, with totals.
  await page.getByRole("link", { name: "‹ Trips" }).click();
  await expect(page.locator(".trip-link")).toHaveCount(2);
  await expect(page.locator(".trip-link").first()).toContainText("Smoke route");
  await expect(page.locator(".stat").filter({ hasText: "Time on the trail" })).toContainText("3 h 30 min");
});
