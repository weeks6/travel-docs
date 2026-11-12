import assert from "node:assert/strict";
import { test } from "node:test";
import { contains, tileArea } from "./map-area.ts";

test("tileArea grows the view to whole tiles plus one around it", () => {
  // Zoom 4: tiles are 22.5° wide.
  assert.deepEqual(tileArea([7.8, 46.4, 8.2, 46.7], 4), [-22.5, 22.5, 45, 90]);
  // Zoom 10: 0.3515625°.
  const a = tileArea([7.8, 46.4, 8.2, 46.7], 10);
  assert.ok(contains(a, [7.8, 46.4, 8.2, 46.7]));
  assert.ok(a[0] <= 7.8 - 0.35 && a[2] >= 8.2 + 0.35);
});

test("small pans stay inside the loaded area, so they need no request", () => {
  const loaded = tileArea([7.8, 46.4, 8.2, 46.7], 10);
  assert.ok(contains(loaded, [7.9, 46.5, 8.3, 46.8]));
  assert.ok(contains(loaded, [7.5, 46.1, 7.9, 46.4]));
  assert.ok(!contains(loaded, [8.5, 46.4, 8.9, 46.7])); // a whole view away
});

test("tileArea stays within the world", () => {
  assert.deepEqual(tileArea([-180, -90, 180, 90], 1), [-180, -90, 180, 90]);
});

test("contains", () => {
  assert.ok(contains([0, 0, 10, 10], [1, 1, 9, 9]));
  assert.ok(contains([0, 0, 10, 10], [0, 0, 10, 10]));
  assert.ok(!contains([0, 0, 10, 10], [1, 1, 11, 9]));
});
