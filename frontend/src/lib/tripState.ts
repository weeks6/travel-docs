// State of the live trip page and how trip messages change it. Kept free of
// React so it can be tested on its own (tripState.test.ts).

import type { Participant, Position, Trip, TripMessage } from "./types";

export type Tracks = Record<string, [number, number][]>; // user → [lon, lat] points

export interface TripState {
  trip: Trip | null;
  participants: Participant[];
  signals: Record<string, number>; // user → time of their last sign of life (ms)
  tracks: Tracks | null; // shown location history, null when hidden
  gone: string; // why the trip can no longer be shown ("" while it can)
  notice: string; // an error to show
}

export const initialTripState: TripState = {
  trip: null,
  participants: [],
  signals: {},
  tracks: null,
  gone: "",
  notice: "",
};

export type TripAction =
  | { type: "message"; message: TripMessage; me: string; at: number }
  | { type: "trip"; trip: Trip }
  | { type: "participants"; participants: Participant[] }
  | { type: "tracks"; tracks: Tracks | null }
  | { type: "gone"; reason: string }
  | { type: "notice"; text: string };

export function tripReducer(s: TripState, a: TripAction): TripState {
  switch (a.type) {
    case "message":
      return applyMessage(s, a.message, a.me, a.at);
    case "trip":
      return { ...s, trip: a.trip };
    case "participants":
      return { ...s, participants: a.participants };
    case "tracks":
      return { ...s, tracks: a.tracks };
    case "gone":
      return { ...s, gone: a.reason };
    case "notice":
      return { ...s, notice: a.text };
  }
}

function applyMessage(s: TripState, m: TripMessage, me: string, at: number): TripState {
  switch (m.type) {
    case "ready":
      return {
        ...s,
        trip: m.trip,
        participants: m.participants,
        signals: Object.fromEntries(m.participants.filter((p) => p.online).map((p) => [p.user_id, at])),
      };
    case "location": {
      const userID = m.user_id;
      const p: Position = {
        lat: m.lat,
        lon: m.lon,
        ele: m.ele,
        accuracy_m: m.accuracy_m,
        speed_mps: m.speed_mps,
        heading_deg: m.heading_deg,
        recorded_at: m.recorded_at,
      };
      return {
        ...s,
        // Positions can arrive out of order (buffered uploads): keep the newest.
        participants: s.participants.map((x) =>
          x.user_id === userID && (!x.last_position || x.last_position.recorded_at <= p.recorded_at)
            ? { ...x, last_position: p, online: true }
            : x,
        ),
        signals: { ...s.signals, [userID]: at },
        tracks: s.tracks ? { ...s.tracks, [userID]: [...(s.tracks[userID] || []), [p.lon, p.lat]] } : s.tracks,
      };
    }
    case "presence":
      return {
        ...s,
        participants: s.participants.map((x) =>
          x.user_id === m.user_id ? { ...x, online: m.online, last_seen_at: m.at } : x,
        ),
        signals: { ...s.signals, [m.user_id]: at },
      };
    case "removed":
      return m.user_id === me ? { ...s, gone: "You are no longer a participant of this trip." } : s;
    case "deleted":
      return { ...s, gone: "This trip was deleted." };
    case "error":
      return { ...s, notice: m.error };
    default:
      // "trip" and "participants" ask for a reload; "pong" needs nothing.
      return s;
  }
}

/** Turns GET /trips/{id}/history into tracks per participant. */
export function tracksFromHistory(fc: GeoJSON.FeatureCollection): Tracks {
  const t: Tracks = {};
  for (const f of fc.features) {
    const userID = (f.properties as { user_id: string }).user_id;
    if (f.geometry.type === "LineString") t[userID] = f.geometry.coordinates as [number, number][];
    else if (f.geometry.type === "Point") t[userID] = [f.geometry.coordinates as [number, number]];
  }
  return t;
}
