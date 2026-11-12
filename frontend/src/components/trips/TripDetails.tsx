"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { ApiError, routesApi, tripsApi } from "@/lib/api";
import { formatDuration, speedKmh } from "@/lib/duration";
import { km, meters, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { ProfilePoint, Trip, TripStats } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import { ElevationChart } from "../ElevationChart";
import Map, { type MapLayer } from "../Map";
import { ConfirmButton, errorMessage } from "../ui";
import { TripFormDialog } from "./TripFormDialog";
import { tripDate } from "./TripsPage";

export function TripDetails() {
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
  return <TripView key={id + user.id} id={id} />;
}

interface RouteData {
  feature: GeoJSON.Feature;
  profile: ProfilePoint[];
  stats: TripStats; // all your trips on this route
}

function TripView({ id }: { id: string }) {
  const router = useRouter();
  const [trip, setTrip] = useState<Trip | null>(null);
  const [route, setRoute] = useState<RouteData | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [fitKey, setFitKey] = useState(0);

  useEffect(() => {
    let active = true;
    tripsApi
      .get(id)
      .then((t) => active && setTrip(t))
      .catch((e) => {
        if (!active) return;
        setError(e instanceof ApiError && e.status === 404 ? "This trip doesn't exist." : errorMessage(e));
      });
    return () => {
      active = false;
    };
  }, [id]);

  // The route's track, profile and your other trips on it.
  const routeID = trip?.route_id ?? null;
  useEffect(() => {
    if (!routeID) return;
    let active = true;
    Promise.all([routesApi.geometry(routeID), routesApi.elevation(routeID), tripsApi.stats({ route_id: routeID })])
      .then(([feature, elevation, stats]) => {
        if (!active) return;
        setRoute({ feature, profile: elevation.points, stats });
        setFitKey((k) => k + 1);
      })
      .catch(() => undefined); // the trip still shows without its map
    return () => {
      active = false;
    };
  }, [routeID]);

  const layers = useMemo<MapLayer[]>(
    () => (route ? [{ key: "route", data: route.feature, style: { color: "#2563eb", weight: 4 } }] : []),
    [route],
  );

  if (error) {
    return (
      <div className="page">
        <p className="msg error">{error}</p>
        <Link href="/trips">‹ Back to trips</Link>
      </div>
    );
  }
  if (!trip) return <div className="page empty">Loading trip…</div>;

  const speed = speedKmh(trip.distance_m, trip.duration_s);
  return (
    <div className="app trip">
      <aside>
        <section>
          <div className="collection-title">
            <Link href="/trips" className="link">
              ‹ Trips
            </Link>
            <h3>{trip.name}</h3>
          </div>
          <div className="meta">{tripDate(trip.date)}</div>
          <div className="stats">
            {(
              [
                ["Time", formatDuration(trip.duration_s)],
                ["Distance", km(trip.distance_m)],
                ["Ascent", meters(trip.ascent_m)],
                ["Average speed", speed ? speed.toFixed(1) + " km/h" : "—"],
              ] as const
            ).map(([label, value]) => (
              <div className="stat" key={label}>
                <div className="v">{value}</div>
                <div className="l">{label}</div>
              </div>
            ))}
          </div>
          {trip.notes && <p className="description">{trip.notes}</p>}
          <div className="actions">
            <button type="button" onClick={() => setEditing(true)}>
              Edit
            </button>
            <ConfirmButton
              label="Delete"
              className="danger"
              question={`Delete the trip “${trip.name}”? Its route is kept.`}
              onConfirm={async () => {
                await tripsApi.remove(trip.id);
                router.push("/trips");
              }}
            />
          </div>
        </section>

        <section className="grow">
          <h4 className="section-title">Route</h4>
          {trip.route_name ? (
            <>
              <p>{trip.route_name}</p>
              {route && route.stats.trips > 1 && (
                <p className="meta">
                  You went on this route {plural(route.stats.trips, "time")}, {formatDuration(route.stats.duration_s)} in
                  total.
                </p>
              )}
              {route && <ElevationChart points={route.profile} />}
            </>
          ) : (
            <p className="meta">The route was deleted or is no longer shared with you; the trip keeps its stats.</p>
          )}
        </section>
      </aside>
      <main>
        <Map layers={layers} fitKey={fitKey} />
      </main>
      {editing && (
        <TripFormDialog
          trip={trip}
          onClose={() => setEditing(false)}
          onSaved={(t) => {
            setEditing(false);
            setTrip(t);
          }}
        />
      )}
    </div>
  );
}
