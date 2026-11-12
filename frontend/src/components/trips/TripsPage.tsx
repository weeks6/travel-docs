"use client";

import { ChevronRight, Footprints, Plus } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { tripsApi } from "@/lib/api";
import { formatDuration } from "@/lib/duration";
import { km, meters, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { Page, Trip, TripStats } from "@/lib/types";
import { cn } from "@/lib/utils";
import { LoginPrompt } from "../account/LoginDialog";
import { EmptyState, ErrorText, PageHeader, PageLoading, PageShell, Pager, Stat, errorMessage } from "../common";
import { Button } from "../ui/button";
import { TripFormDialog } from "./TripFormDialog";

export function TripsPage() {
  const { user, ready } = useSession();
  if (!ready) return <PageLoading />;
  if (!user) {
    return (
      <PageShell>
        <PageHeader title="Trips" />
        <LoginPrompt
          title="Your trail log"
          text="Log in to keep a log of your trips: which route, when, and how long it took."
        />
      </PageShell>
    );
  }
  return <Trips key={user.id} />;
}

/** Parts of a trip date (a local calendar day, YYYY-MM-DD). */
function dateParts(d: string) {
  const [y, m, day] = d.split("-").map(Number);
  const date = new Date(y, m - 1, day);
  return {
    date,
    weekday: date.toLocaleDateString(undefined, { weekday: "short" }),
    month: date.toLocaleDateString(undefined, { month: "short" }),
    day,
    year: y,
  };
}

/** Long dates, e.g. "Sat, 1 May 2026". */
export function tripDate(d: string): string {
  return dateParts(d).date.toLocaleDateString(undefined, {
    weekday: "short",
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

/** A calendar-page tile for a trip date. */
export function DateTile({ date, className }: { date: string; className?: string }) {
  const p = dateParts(date);
  return (
    <div
      className={cn(
        "grid w-14 shrink-0 overflow-hidden rounded-xl bg-card text-center shadow-soft ring-1 ring-foreground/8",
        className,
      )}
      aria-hidden
    >
      <div className="bg-primary py-0.5 text-[10px] font-semibold tracking-wider text-primary-foreground uppercase">
        {p.month}
      </div>
      <div className="figure pt-1 text-xl leading-none">{p.day}</div>
      <div className="pb-1 text-[10px] text-muted-foreground">{p.year}</div>
    </div>
  );
}

export function StatsRow({ stats }: { stats: TripStats | null }) {
  if (!stats || stats.trips === 0) return null;
  return (
    <div className="topo overflow-hidden rounded-2xl bg-card p-5 shadow-soft ring-1 ring-foreground/8 sm:p-6">
      <div className="grid grid-cols-2 gap-5 sm:grid-cols-4">
        <Stat label={stats.trips === 1 ? "Trip" : "Trips"} value={String(stats.trips)} className="[&_.figure]:text-3xl" />
        <Stat label="Time on the trail" value={formatDuration(stats.duration_s)} className="[&_.figure]:text-3xl" />
        <Stat label="Distance" value={km(stats.distance_m)} className="[&_.figure]:text-3xl" />
        <Stat label="Ascent" value={meters(stats.ascent_m)} className="[&_.figure]:text-3xl" />
      </div>
    </div>
  );
}

function Trips() {
  const router = useRouter();
  const [year, setYear] = useState(0); // 0 = all years
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Trip> | null>(null);
  const [stats, setStats] = useState<TripStats | null>(null);
  const [overall, setOverall] = useState<TripStats | null>(null); // all years, for the year picker
  const [error, setError] = useState("");
  const [logging, setLogging] = useState(false);

  useEffect(() => {
    let active = true;
    const filter = { year: year || undefined };
    Promise.all([tripsApi.list({ ...filter, page, page_size: 20 }), tripsApi.stats(filter), tripsApi.stats()])
      .then(([list, st, all]) => {
        if (!active) return;
        setRes(list);
        setStats(st);
        setOverall(all);
        setError("");
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [year, page]);

  const years = useMemo(() => {
    if (!overall?.first_date || !overall.last_date) return [];
    const first = Number(overall.first_date.slice(0, 4));
    const last = Number(overall.last_date.slice(0, 4));
    return Array.from({ length: last - first + 1 }, (_, i) => last - i);
  }, [overall]);

  return (
    <PageShell>
      <PageHeader
        title="Trips"
        description="Your log of the routes you went on: when, and how long it took."
        actions={
          <Button variant="sunset" size="lg" onClick={() => setLogging(true)}>
            <Plus data-icon="inline-start" /> Log a trip
          </Button>
        }
      />
      {years.length > 0 && (
        <div className="-mx-1 flex gap-1 overflow-x-auto px-1 pb-1" role="group" aria-label="Year">
          {[0, ...years].map((y) => (
            <Button
              key={y}
              size="sm"
              variant={year === y ? "default" : "outline"}
              aria-pressed={year === y}
              onClick={() => {
                setPage(1);
                setYear(y);
              }}
            >
              {y || "All years"}
            </Button>
          ))}
        </div>
      )}
      <StatsRow stats={stats} />
      {error && <ErrorText>{error}</ErrorText>}

      {res && res.items.length === 0 ? (
        <EmptyState icon={<Footprints className="size-5" />} title={year ? `No trips in ${year}` : "No trips yet"}>
          {!year && (
            <>
              <p>Log one from a GPX file or one of your routes.</p>
              <Button className="mt-4" onClick={() => setLogging(true)}>
                <Plus data-icon="inline-start" /> Log your first trip
              </Button>
            </>
          )}
        </EmptyState>
      ) : (
        <ul className="grid gap-2">
          {res?.items.map((t) => (
            <li key={t.id}>
              <Link
                href={`/trips/${t.id}`}
                className="trip-link group flex items-center gap-4 rounded-2xl bg-card p-3 pr-4 shadow-soft ring-1 ring-foreground/8 transition-all hover:-translate-y-px hover:ring-primary/30"
              >
                <DateTile date={t.date} />
                <div className="min-w-0 flex-1">
                  <div className="truncate font-heading text-base font-semibold">{t.name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {dateParts(t.date).weekday}
                    {t.route_name && t.route_name !== t.name && ` · ${t.route_name}`}
                    {t.notes && ` · ${t.notes}`}
                  </div>
                </div>
                <div className="hidden gap-6 text-right sm:flex">
                  <Stat label="time" value={formatDuration(t.duration_s)} className="[&_.figure]:text-base" />
                  <Stat label="distance" value={km(t.distance_m)} className="[&_.figure]:text-base" />
                </div>
                <div className="text-right text-xs text-muted-foreground tabular-nums sm:hidden">
                  {formatDuration(t.duration_s)}
                  <br />
                  {km(t.distance_m)}
                </div>
                <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
              </Link>
            </li>
          ))}
          {!res && !error && [0, 1, 2].map((i) => <li key={i} className="h-20 animate-pulse rounded-2xl bg-muted" />)}
        </ul>
      )}
      {res && res.total > 0 && (
        <p className="-mt-2 text-xs text-muted-foreground">
          {plural(res.total, "trip")}
          {year ? ` in ${year}` : ""}
        </p>
      )}
      <Pager res={res} onPage={setPage} />
      {logging && (
        <TripFormDialog
          onClose={() => setLogging(false)}
          onSaved={(t) => {
            setLogging(false);
            router.push(`/trips/${t.id}`);
          }}
        />
      )}
    </PageShell>
  );
}
