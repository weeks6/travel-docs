// Run with `npm test` (Node's test runner; Node strips the types).
import assert from "node:assert/strict";
import { test } from "node:test";
import { initialTripState, tracksFromHistory, tripReducer, type TripState } from "./tripState.ts";
import type { Participant, Trip, TripMessage } from "./types";

const trip = { id: "t1", name: "Hike", status: "active" } as Trip;
const alice = { user_id: "a", display_name: "Alice", online: true, last_position: null } as Participant;
const bob = { user_id: "b", display_name: "Bob", online: false, last_position: null } as Participant;

function apply(s: TripState, ...msgs: TripMessage[]): TripState {
  return msgs.reduce((st, m, i) => tripReducer(st, { type: "message", message: m, me: "a", at: 1000 + i }), s);
}

const ready: TripMessage = { type: "ready", user_id: "a", trip, participants: [alice, bob] };

test("ready loads the snapshot and marks online participants as alive", () => {
  const s = apply(initialTripState, ready);
  assert.equal(s.trip, trip);
  assert.deepEqual(s.participants, [alice, bob]);
  assert.deepEqual(s.signals, { a: 1000 });
});

test("locations update the participant, keep the newest and extend shown tracks", () => {
  let s = apply(initialTripState, ready);
  s = tripReducer(s, { type: "tracks", tracks: {} });
  s = apply(
    s,
    { type: "location", user_id: "b", lat: 1, lon: 2, recorded_at: "2026-01-01T10:00:00Z" },
    { type: "location", user_id: "b", lat: 9, lon: 9, recorded_at: "2026-01-01T09:00:00Z" }, // older
  );
  const b = s.participants.find((p) => p.user_id === "b")!;
  assert.deepEqual([b.last_position?.lat, b.last_position?.lon, b.last_position?.recorded_at], [1, 2, "2026-01-01T10:00:00Z"]);
  assert.equal(b.online, true);
  assert.deepEqual(s.tracks, { b: [[2, 1], [9, 9]] });
  assert.equal(s.signals.b, 1001);
});

test("hidden tracks stay hidden", () => {
  const s = apply(initialTripState, ready, { type: "location", user_id: "b", lat: 1, lon: 2, recorded_at: "x" });
  assert.equal(s.tracks, null);
});

test("presence, removal, deletion and errors", () => {
  let s = apply(initialTripState, ready, { type: "presence", user_id: "b", online: true, at: "now" });
  assert.equal(s.participants[1].online, true);
  assert.equal(s.participants[1].last_seen_at, "now");

  assert.equal(apply(s, { type: "removed", user_id: "b" }).gone, "");
  assert.match(apply(s, { type: "removed", user_id: "a" }).gone, /no longer a participant/);
  assert.match(apply(s, { type: "deleted" }).gone, /deleted/);
  assert.equal(apply(s, { type: "error", error: "slow down", code: "rate_limited" }).notice, "slow down");

  s = apply(s, { type: "trip" }, { type: "participants" }, { type: "pong" });
  assert.equal(s.participants.length, 2); // reload requests leave state alone
});

test("tracksFromHistory", () => {
  const t = tracksFromHistory({
    type: "FeatureCollection",
    features: [
      { type: "Feature", properties: { user_id: "a" }, geometry: { type: "LineString", coordinates: [[1, 2], [3, 4]] } },
      { type: "Feature", properties: { user_id: "b" }, geometry: { type: "Point", coordinates: [5, 6] } },
    ],
  });
  assert.deepEqual(t, { a: [[1, 2], [3, 4]], b: [[5, 6]] });
});
