"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { tripsApi } from "@/lib/api";
import { formatDuration } from "@/lib/duration";
import { km, meters, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { Page, Trip, TripStats } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import { Pager, errorMessage } from "../ui";
import { TripFormDialog } from "./TripFormDialog";

export function TripsPage() {
  const { user, ready } = useSession();
  if (!ready) return <div className="page empty">Loading…</div>;
  if (!user) {
    return (
      <div className="page">
        <h1>Trips</h1>
        <LoginPrompt text="Log in to keep a log of your trips: which route, when, and how long it took." />
      </div>
    );
  }
  return <Trips key={user.id} />;
}

/** Long dates for the list, e.g. "Sat, 1 May 2026" (dates are local calendar days). */
export function tripDate(d: string): string {
  const [y, m, day] = d.split("-").map(Number);
  return new Date(y, m - 1, day).toLocaleDateString(undefined, {
    weekday: "short",
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

export function StatsRow({ stats }: { stats: TripStats | null }) {
  if (!stats || stats.trips === 0) return null;
  return (
    <div className="stats">
      {(
        [
          [stats.trips === 1 ? "Trip" : "Trips", String(stats.trips)],
          ["Time on the trail", formatDuration(stats.duration_s)],
          ["Distance", km(stats.distance_m)],
          ["Ascent", meters(stats.ascent_m)],
        ] as const
      ).map(([label, value]) => (
        <div className="stat" key={label}>
          <div className="v">{value}</div>
          <div className="l">{label}</div>
        </div>
      ))}
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
    <div className="page">
      <div className="page-head">
        <h1>Trips</h1>
        <button type="button" className="primary" onClick={() => setLogging(true)}>
          Log a trip
        </button>
      </div>
      <p className="meta">Your log of the routes you went on: when, and how long it took.</p>
      {error && <p className="msg error">{error}</p>}

      <div className="row toolbar">
        <select
          aria-label="Year"
          value={year}
          onChange={(e) => {
            setPage(1);
            setYear(Number(e.target.value));
          }}
        >
          <option value={0}>All years</option>
          {years.map((y) => (
            <option key={y} value={y}>
              {y}
            </option>
          ))}
        </select>
      </div>
      <StatsRow stats={stats} />

      <ul className="list cards">
        {res?.items.map((t) => (
          <li key={t.id}>
            <Link href={`/trips/${t.id}`} className="trip-link">
              <div className="name">{t.name}</div>
              <div className="meta">
                {tripDate(t.date)} · {formatDuration(t.duration_s)} · {km(t.distance_m)}
                {t.route_name && t.route_name !== t.name && ` · ${t.route_name}`}
              </div>
            </Link>
          </li>
        ))}
        {res && res.items.length === 0 && (
          <div className="empty">
            {year ? `No trips in ${year}.` : "No trips yet. Log one from a GPX file or one of your routes."}
          </div>
        )}
      </ul>
      {res && res.total > 0 && <p className="meta">{plural(res.total, "trip")}</p>}
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
    </div>
  );
}
