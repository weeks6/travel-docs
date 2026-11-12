// Run with `npm test` (Node's test runner; Node strips the types).
import assert from "node:assert/strict";
import { test } from "node:test";
import { formatDuration, fromHoursMinutes, speedKmh, toHoursMinutes } from "./duration.ts";

test("formatDuration", () => {
  assert.equal(formatDuration(30), "30 s");
  assert.equal(formatDuration(45 * 60), "45 min");
  assert.equal(formatDuration(95 * 60), "1 h 35 min");
  assert.equal(formatDuration(2 * 3600), "2 h");
  assert.equal(formatDuration(51 * 3600), "2 d 3 h");
  assert.equal(formatDuration(48 * 3600), "2 d");
});

test("hours and minutes round-trip", () => {
  assert.deepEqual(toHoursMinutes(95 * 60 + 20), { hours: 1, minutes: 35 });
  assert.equal(fromHoursMinutes("1", "35"), 95 * 60);
  assert.equal(fromHoursMinutes("", "40"), 40 * 60);
  assert.equal(fromHoursMinutes("3", ""), 3 * 3600);
  assert.equal(fromHoursMinutes(" ", ""), null);
});

test("invalid durations", () => {
  for (const [h, m] of [["0", "0"], ["-1", "0"], ["1", "60"], ["1.5", "0"], ["x", ""]]) {
    assert.ok(Number.isNaN(fromHoursMinutes(h, m)), `${h}:${m}`);
  }
});

test("speedKmh", () => {
  assert.equal(speedKmh(10_000, 7200), 5);
  assert.equal(speedKmh(0, 3600), null);
});
