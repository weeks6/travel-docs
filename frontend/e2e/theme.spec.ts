import { expect, test, type Page } from "@playwright/test";

// The theme switcher: light, dark or the system setting, remembered per
// browser and applied before the first paint.

const isDark = (page: Page) => page.evaluate(() => document.documentElement.classList.contains("dark"));

async function choose(page: Page, label: "Light" | "Dark" | "System") {
  await page.getByRole("button", { name: "Theme" }).click();
  await page.getByRole("menuitemradio", { name: label }).click();
}

test("theme follows the system until chosen, and the choice sticks", async ({ page }) => {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));

  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  expect(await isDark(page)).toBe(true); // system default

  await choose(page, "Light");
  expect(await isDark(page)).toBe(false);
  await expect(page.locator("body")).toHaveCSS("color-scheme", "light");

  // Applied by the head script on load, before React runs.
  await page.goto("/trips", { waitUntil: "commit" });
  await page.waitForSelector("body", { state: "attached" });
  expect(await isDark(page)).toBe(false);
  await page.waitForLoadState("load");
  await page.getByRole("button", { name: "Theme" }).click();
  await expect(page.getByRole("menuitemradio", { name: "Light" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Escape");

  // System again: follows the OS, including later changes.
  await choose(page, "System");
  expect(await isDark(page)).toBe(true);
  await page.emulateMedia({ colorScheme: "light" });
  await expect.poll(() => isDark(page)).toBe(false);

  await choose(page, "Dark");
  expect(await isDark(page)).toBe(true);
  await page.reload();
  expect(await isDark(page)).toBe(true);

  expect(errors.filter((e) => /hydrat/i.test(e))).toEqual([]);
});
