"use client";

import { ChevronLeft, ExternalLink, Footprints, Pencil, Trash2 } from "lucide-react";
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
import Map, { routeStyle, type MapLayer } from "../Map";
import { ConfirmButton, EmptyState, PageLoading, PageShell, StatGrid, errorMessage } from "../common";
import { Button } from "../ui/button";
import { Separator } from "../ui/separator";
import { TripFormDialog } from "./TripFormDialog";
import { DateTile, tripDate } from "./TripsPage";

export function TripDetails() {
  const { id } = useParams<{ id: string }>();
  const { user, ready } = useSession();
  if (!ready) return <PageLoading />;
  if (!user) {
    return (
      <PageShell>
        <LoginPrompt text="Log in to see this trip." />
      </PageShell>
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

  const layers = useMemo<MapLayer[]>(() => (route ? [{ key: "route", data: route.feature, style: routeStyle }] : []), [route]);

  if (error) {
    return (
      <PageShell>
        <EmptyState icon={<Footprints className="size-5" />} title={error}>
          <Button asChild variant="outline" className="mt-4">
            <Link href="/trips">
              <ChevronLeft data-icon="inline-start" /> Back to trips
            </Link>
          </Button>
        </EmptyState>
      </PageShell>
    );
  }
  if (!trip) return <PageLoading label="Loading trip…" />;

  const speed = speedKmh(trip.distance_m, trip.duration_s);
  return (
    <div className="relative flex flex-col gap-3 p-3 lg:h-full lg:p-0">
      <div className="order-2 h-[55vh] overflow-hidden rounded-2xl ring-1 ring-foreground/8 lg:absolute lg:inset-0 lg:h-full lg:rounded-none lg:ring-0">
        <Map layers={layers} fitKey={fitKey} />
      </div>
      <aside className="order-1 grid content-start gap-5 overflow-y-auto rounded-2xl bg-card/95 p-5 shadow-soft ring-1 ring-foreground/8 backdrop-blur-md lg:absolute lg:top-4 lg:bottom-4 lg:left-4 lg:z-10 lg:w-[400px]">
        <Button asChild variant="ghost" size="sm" className="-ml-2 w-fit">
          <Link href="/trips" aria-label="Back to trips">
            <ChevronLeft data-icon="inline-start" /> All trips
          </Link>
        </Button>
        <div className="flex items-start gap-4">
          <DateTile date={trip.date} className="w-16" />
          <div className="min-w-0">
            <h1 className="text-2xl leading-tight font-semibold">{trip.name}</h1>
            <p className="mt-1 text-sm text-muted-foreground">{tripDate(trip.date)}</p>
          </div>
        </div>
        <StatGrid
          className="sm:grid-cols-2"
          stats={[
            ["Time", formatDuration(trip.duration_s)],
            ["Distance", km(trip.distance_m)],
            ["Ascent", meters(trip.ascent_m)],
            ["Average speed", speed ? speed.toFixed(1) + " km/h" : "—"],
          ]}
        />
        {trip.notes && (
          <blockquote className="border-l-2 border-sunset pl-3 text-sm whitespace-pre-line text-foreground/90">
            {trip.notes}
          </blockquote>
        )}
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => setEditing(true)}>
            <Pencil data-icon="inline-start" /> Edit
          </Button>
          <ConfirmButton
            label="Delete"
            variant="ghost"
            destructive
            icon={<Trash2 />}
            question={`Delete the trip “${trip.name}”? Its route is kept.`}
            onConfirm={async () => {
              await tripsApi.remove(trip.id);
              router.push("/trips");
            }}
          />
        </div>

        <Separator />
        <section className="grid gap-3">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Route</h2>
            {trip.route_id && (
              <Button asChild variant="link" size="sm" className="h-auto p-0">
                <Link href={`/?route=${trip.route_id}`}>
                  Open route <ExternalLink data-icon="inline-end" />
                </Link>
              </Button>
            )}
          </div>
          {trip.route_name ? (
            <>
              <p className="font-medium">{trip.route_name}</p>
              {route && route.stats.trips > 1 && (
                <p className="rounded-xl bg-accent px-3 py-2 text-sm text-accent-foreground">
                  You went on this route {plural(route.stats.trips, "time")}, {formatDuration(route.stats.duration_s)} in
                  total.
                </p>
              )}
              {route && <ElevationChart points={route.profile} />}
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              The route was deleted or is no longer shared with you; the trip keeps its stats.
            </p>
          )}
        </section>
      </aside>
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
