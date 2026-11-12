"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { ApiError, routesApi, tripsApi } from "@/lib/api";
import { ago } from "@/lib/format";
import { useSession } from "@/lib/session";
import { initialTripState, tracksFromHistory, tripReducer } from "@/lib/tripState";
import { useTripSocket } from "@/lib/tripSocket";
import type { Participant, Position, Trip, TripMessage, User } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import Map, { type MapLayer, type MapMarker } from "../Map";
import { ConfirmButton, Msg, errorMessage, useAction } from "../ui";
import { TripFormDialog, statusLabels } from "./TripFormDialog";
import { InviteDialog } from "./InviteDialog";

// Matches the server: a participant without a sign of life for this long is offline.
const PRESENCE_TIMEOUT = 75_000;

// Positions older than this are drawn faded.
const STALE_POSITION = 5 * 60_000;

// The server accepts 2 updates per second; one every 2 s is plenty for walking.
const MIN_SEND_INTERVAL = 2000;

const COLORS = ["#2563eb", "#dc2626", "#16a34a", "#9333ea", "#ea580c", "#0891b2", "#db2777", "#65a30d", "#7c3aed", "#ca8a04"];

export function TripView() {
  const { id } = useParams<{ id: string }>();
  const { user, ready } = useSession();
  if (!ready) return <div className="page empty">Loading…</div>;
  if (!user) {
    return (
      <div className="page">
        <LoginPrompt text="Log in to see this trip." />
      </div>
    );
  }
  return <TripLive key={id + user.id} id={id} me={user} />;
}

function TripLive({ id, me }: { id: string; me: User }) {
  const router = useRouter();
  // Trip, participants and presence, updated by live messages (lib/tripState).
  const [state, dispatch] = useReducer(tripReducer, initialTripState);
  const { trip, participants, signals, tracks, gone, notice } = state;
  const setTrip = useCallback((t: Trip) => dispatch({ type: "trip", trip: t }), []);
  const setNotice = useCallback((text: string) => dispatch({ type: "notice", text }), []);
  const [route, setRoute] = useState<{ id: string; feature: GeoJSON.Feature } | null>(null);
  const [fitKey, setFitKey] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const [sharing, setSharing] = useState<"off" | "gps" | "simulate">("off");
  const [dialog, setDialog] = useState<null | "edit" | "invite">(null);
  const fitted = useRef(false);

  const loadTrip = useCallback(() => {
    tripsApi
      .get(id)
      .then(setTrip)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 404)
          dispatch({ type: "gone", reason: "This trip doesn't exist, or you are not part of it." });
        else setNotice(errorMessage(e));
      });
  }, [id, setTrip, setNotice]);
  const loadParticipants = useCallback(() => {
    tripsApi
      .participants(id)
      .then((r) => dispatch({ type: "participants", participants: r.items }))
      .catch(() => undefined);
  }, [id]);
  useEffect(() => {
    loadTrip();
  }, [loadTrip]);

  const onMessage = (m: TripMessage) => {
    dispatch({ type: "message", message: m, me: me.id, at: Date.now() });
    if (m.type === "ready" && !fitted.current) {
      fitted.current = true;
      setFitKey((k) => k + 1);
    } else if (m.type === "trip") loadTrip();
    else if (m.type === "participants") loadParticipants();
  };
  const socket = useTripSocket(id, gone === "", onMessage);

  // Re-render periodically so presence and "x min ago" stay current.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 10_000);
    return () => clearInterval(t);
  }, []);

  // The planned route, if we may see it.
  const routeID = trip?.route_id ?? null;
  useEffect(() => {
    if (!routeID) return;
    let active = true;
    routesApi
      .geometry(routeID)
      .then((f) => {
        if (!active) return;
        setRoute({ id: routeID, feature: f });
        setFitKey((k) => k + 1);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [routeID]);
  const plannedRoute = route && route.id === routeID ? route.feature : null;

  // Share my location while the trip is live.
  const live = socket.state === "live" && trip?.status === "active";
  const myPosition = participants.find((p) => p.user_id === me.id)?.last_position;
  const simStart = useRef<[number, number]>([46.5, 8]);
  useEffect(() => {
    if (myPosition) simStart.current = [myPosition.lat, myPosition.lon];
    else if (plannedRoute?.geometry.type === "MultiLineString") {
      const c = plannedRoute.geometry.coordinates[0]?.[0];
      if (c) simStart.current = [c[1], c[0]];
    }
  }, [myPosition, plannedRoute]);
  const { send } = socket;
  useEffect(() => {
    if (!live || sharing === "off") return;
    let last = 0;
    const report = (p: Omit<Position, "recorded_at">) => {
      const t = Date.now();
      if (t - last < MIN_SEND_INTERVAL) return;
      last = t;
      send({ type: "location", ...p, recorded_at: new Date(t).toISOString() });
    };
    if (sharing === "gps") {
      const finite = (v: number | null) => (v !== null && Number.isFinite(v) ? v : undefined);
      const watch = navigator.geolocation.watchPosition(
        (pos) => {
          const c = pos.coords;
          report({
            lat: c.latitude,
            lon: c.longitude,
            accuracy_m: finite(c.accuracy),
            ele: finite(c.altitude),
            speed_mps: finite(c.speed),
            heading_deg: finite(c.heading),
          });
        },
        (err) => {
          setNotice("Location unavailable: " + err.message);
          setSharing("off");
        },
        { enableHighAccuracy: true, maximumAge: 5000, timeout: 30_000 },
      );
      return () => navigator.geolocation.clearWatch(watch);
    }
    // Simulated walk, for trying the trip without moving.
    let [lat, lon] = simStart.current;
    let heading = Math.random() * 2 * Math.PI;
    const tick = setInterval(() => {
      heading += (Math.random() - 0.5) * 0.6;
      lat += Math.cos(heading) * 0.00003;
      lon += Math.sin(heading) * 0.00004;
      report({ lat, lon, accuracy_m: 8, speed_mps: 1.4, heading_deg: ((heading * 180) / Math.PI + 360) % 360 });
    }, MIN_SEND_INTERVAL);
    return () => clearInterval(tick);
  }, [live, sharing, send, setNotice]);

  const toggleTracks = () => {
    if (tracks) {
      dispatch({ type: "tracks", tracks: null });
      return;
    }
    tripsApi
      .history(id)
      .then((fc) => dispatch({ type: "tracks", tracks: tracksFromHistory(fc) }))
      .catch((e) => setNotice(errorMessage(e)));
  };

  const colorOf = useMemo(() => {
    const m: Record<string, string> = {};
    participants.forEach((p, i) => (m[p.user_id] = COLORS[i % COLORS.length]));
    return m;
  }, [participants]);
  const isOnline = (p: Participant) => p.online && now - (signals[p.user_id] ?? 0) < PRESENCE_TIMEOUT;

  const layers = useMemo(() => {
    const l: MapLayer[] = [];
    if (plannedRoute) l.push({ key: "route", data: plannedRoute, style: { color: "#475569", weight: 4, opacity: 0.6, dashArray: "8 6" } });
    for (const [userID, coords] of Object.entries(tracks || {})) {
      if (coords.length < 2) continue;
      l.push({
        key: "track-" + userID,
        data: { type: "LineString", coordinates: coords } as GeoJSON.LineString,
        style: { color: colorOf[userID] || "#64748b", weight: 3, opacity: 0.8 },
      });
    }
    return l;
  }, [plannedRoute, tracks, colorOf]);

  const markers = useMemo(
    () =>
      participants
        .filter((p) => p.last_position)
        .map(
          (p): MapMarker => ({
            id: p.user_id,
            lat: p.last_position!.lat,
            lon: p.last_position!.lon,
            accuracy: p.last_position!.accuracy_m,
            label: p.user_id === me.id ? `${p.display_name} (you)` : p.display_name,
            color: colorOf[p.user_id],
            faded:
              !(p.online && now - (signals[p.user_id] ?? 0) < PRESENCE_TIMEOUT) ||
              now - new Date(p.last_position!.recorded_at).getTime() > STALE_POSITION,
          }),
        ),
    [participants, signals, now, colorOf, me.id],
  );

  if (gone) {
    return (
      <div className="page">
        <p className="msg error">{gone}</p>
        <Link href="/trips">‹ Back to trips</Link>
      </div>
    );
  }
  if (!trip) return <div className="page empty">{notice || "Loading trip…"}</div>;

  const isOwner = trip.my_role === "owner";
  return (
    <div className="app trip">
      <aside>
        <section>
          <div className="collection-title">
            <Link href="/trips" className="link">
              ‹ Trips
            </Link>
            <h3>{trip.name}</h3>
            <span className={"badge status-" + trip.status}>{statusLabels[trip.status]}</span>
          </div>
          <div className="meta">
            {isOwner ? "Your trip" : `By ${trip.owner_name}`}
            {trip.route_name && ` · route: ${trip.route_name}`} · history kept {trip.location_retention_days} days
          </div>
          {trip.description && <p className="description">{trip.description}</p>}
          <ConnectionState state={socket.state} reason={socket.closedReason} />
          {isOwner && (
            <div className="actions">
              {trip.status === "planned" && (
                <StatusButton tripId={trip.id} action="start" label="Start trip" onDone={setTrip} />
              )}
              {trip.status === "active" && (
                <ConfirmButton
                  label="End trip"
                  question="End the trip? Location sharing stops for everyone; the history stays available."
                  onConfirm={async () => setTrip(await tripsApi.end(trip.id))}
                />
              )}
              {trip.status !== "ended" && (
                <button type="button" onClick={() => setDialog("invite")}>
                  Invite
                </button>
              )}
              <button type="button" onClick={() => setDialog("edit")}>
                Edit
              </button>
              <ConfirmButton
                label="Delete"
                className="danger"
                question={`Delete “${trip.name}” with all its location history?`}
                onConfirm={async () => {
                  await tripsApi.remove(trip.id);
                  router.push("/trips");
                }}
              />
            </div>
          )}
        </section>

        <section>
          <h4 className="section-title">Share my location</h4>
          {trip.status !== "active" ? (
            <p className="meta">
              {trip.status === "planned"
                ? "Location sharing starts when the owner starts the trip."
                : "The trip has ended."}
            </p>
          ) : (
            <>
              <div className="row">
                <select
                  value={sharing}
                  onChange={(e) => {
                    const v = e.target.value as typeof sharing;
                    if (v === "gps" && !("geolocation" in navigator)) {
                      setNotice("This browser can't share its location.");
                      return;
                    }
                    setNotice("");
                    setSharing(v);
                  }}
                >
                  <option value="off">Not sharing</option>
                  <option value="gps">Share my GPS location</option>
                  <option value="simulate">Simulate a walk (demo)</option>
                </select>
              </div>
              <p className="meta">
                {sharing === "off"
                  ? "Others see your last shared position."
                  : live
                    ? "Sharing while this page is open."
                    : "Waiting for the connection…"}
              </p>
            </>
          )}
          {notice && <p className="msg error">{notice}</p>}
        </section>

        <section className="grow">
          <div className="row">
            <h4 className="section-title">Participants ({participants.length})</h4>
            <button type="button" className="link" onClick={toggleTracks}>
              {tracks ? "Hide tracks" : "Show tracks"}
            </button>
            <button type="button" className="link" onClick={() => setFitKey((k) => k + 1)}>
              Fit map
            </button>
          </div>
          <ul className="list participants">
            {participants.map((p) => (
              <li key={p.user_id}>
                <span className="dot" style={{ background: colorOf[p.user_id] }} />
                <div className="grow">
                  <div className="name">
                    {p.display_name}
                    {p.user_id === me.id && " (you)"}
                    {p.role === "owner" && <span className="badge">owner</span>}
                  </div>
                  <div className="meta">
                    <span className={isOnline(p) ? "online" : "offline"}>{isOnline(p) ? "online" : "offline"}</span>
                    {!isOnline(p) && p.last_seen_at && ` · seen ${ago(p.last_seen_at, now)}`}
                    {p.last_position
                      ? ` · position ${ago(p.last_position.recorded_at, now)}`
                      : " · no position yet"}
                    {p.last_position?.speed_mps != null && ` · ${(p.last_position.speed_mps * 3.6).toFixed(1)} km/h`}
                  </div>
                </div>
                {isOwner && p.user_id !== me.id && (
                  <ConfirmButton
                    label="Remove"
                    className="link"
                    question={`Remove ${p.display_name} from the trip? Their location history in this trip is deleted.`}
                    onConfirm={() => tripsApi.removeParticipant(trip.id, p.user_id)}
                  />
                )}
              </li>
            ))}
          </ul>
          {!isOwner && (
            <ConfirmButton
              label="Leave trip"
              className="danger"
              question="Leave this trip? Your location history in it is deleted."
              onConfirm={async () => {
                await tripsApi.removeParticipant(trip.id, me.id);
                router.push("/trips");
              }}
            />
          )}
        </section>
      </aside>
      <main>
        <Map layers={layers} markers={markers} fitKey={fitKey} />
      </main>
      {dialog === "edit" && (
        <TripFormDialog
          trip={trip}
          onClose={() => setDialog(null)}
          onSaved={(t) => {
            setDialog(null);
            setTrip(t);
          }}
        />
      )}
      {dialog === "invite" && <InviteDialog trip={trip} onClose={() => setDialog(null)} />}
    </div>
  );
}

function ConnectionState({ state, reason }: { state: string; reason: string }) {
  const label =
    state === "live" ? "Live" : state === "connecting" ? "Connecting…" : state === "reconnecting" ? "Reconnecting…" : reason;
  return (
    <div className={"conn conn-" + state}>
      <span className="dot" /> {label}
    </div>
  );
}

function StatusButton({
  tripId,
  action,
  label,
  onDone,
}: {
  tripId: string;
  action: "start" | "end";
  label: string;
  onDone: (t: Trip) => void;
}) {
  const { pending, msg, run } = useAction();
  return (
    <>
      <button
        type="button"
        className="primary"
        disabled={pending}
        onClick={() => run(async () => onDone(await tripsApi[action](tripId)))}
      >
        {label}
      </button>
      <Msg msg={msg} />
    </>
  );
}
