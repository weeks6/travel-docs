"use client";

import { useEffect, useMemo, useState } from "react";
import { type RouteQuery, routesApi } from "@/lib/api";
import { date, km, meters } from "@/lib/format";
import type { Page, Route, User } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import { ImportForm } from "../ImportForm";
import { type BBox } from "../Map";
import { Pager, RoleBadge, errorMessage } from "../ui";

export function RouteItem({ r, selected, onSelect }: { r: Route; selected: boolean; onSelect: (r: Route) => void }) {
  return (
    <li className={selected ? "selected" : undefined} onClick={() => onSelect(r)}>
      <div className="name">
        {r.name}
        {r.visibility === "public" && <span className="badge public">public</span>}
        {r.my_role !== "owner" && <RoleBadge role={r.my_role} />}
      </div>
      <div className="meta">
        {km(r.distance_m)} · ↑ {meters(r.ascent_m)} · {date(r.started_at || r.created_at)}
        {r.my_role !== "owner" && ` · by ${r.owner_name}`}
      </div>
    </li>
  );
}

export interface Filters {
  scope: string;
  name: string;
  minKm: string;
  maxKm: string;
  sort: string;
  inView: boolean;
}

export function RoutesPanel({
  user,
  selectedId,
  onSelect,
  reloadKey,
  bbox,
  onImported,
}: {
  user: User | null;
  selectedId?: string;
  onSelect: (r: Route) => void;
  reloadKey: number;
  bbox: BBox | null;
  onImported: (routeId: string | null) => void;
}) {
  const [filters, setFilters] = useState<Filters>({
    scope: user ? "mine" : "public",
    name: "",
    minKm: "",
    maxKm: "",
    sort: "created_at:desc",
    inView: false,
  });
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Route> | null>(null);
  const [error, setError] = useState("");

  const query = useMemo((): RouteQuery => {
    const [sort, order] = filters.sort.split(":");
    return {
      page,
      page_size: 20,
      scope: filters.scope,
      sort,
      order,
      name: filters.name,
      min_distance_m: filters.minKm ? Number(filters.minKm) * 1000 : undefined,
      max_distance_m: filters.maxKm ? Number(filters.maxKm) * 1000 : undefined,
      bbox: filters.inView && bbox ? bbox.join(",") : undefined,
    };
  }, [page, filters, bbox]);

  useEffect(() => {
    let active = true;
    routesApi
      .list(query)
      .then((r) => {
        if (!active) return;
        if (r.page > 1 && r.items.length === 0) setPage(r.total_pages || 1);
        else {
          setRes(r);
          setError("");
        }
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [query, reloadKey]);

  const scopes = user
    ? [
        ["mine", "My routes"],
        ["shared", "Shared with me"],
        ["public", "Public routes"],
        ["all", "Everything I can see"],
      ]
    : [["public", "Public routes"]];

  return (
    <div className="panel">
      {user ? (
        <section>
          <ImportForm onSucceeded={(job) => onImported(job.route_id)} />
        </section>
      ) : (
        <LoginPrompt text="Want to upload your own GPX tracks?" />
      )}
      <section>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const fd = new FormData(e.currentTarget);
            setPage(1);
            setFilters((f) => ({
              ...f,
              name: String(fd.get("name") || "").trim(),
              minKm: String(fd.get("min_km") || ""),
              maxKm: String(fd.get("max_km") || ""),
              sort: String(fd.get("sort")),
              inView: fd.get("in_view") === "on",
            }));
          }}
        >
          <div className="row">
            <select
              value={filters.scope}
              onChange={(e) => {
                setPage(1);
                setFilters((f) => ({ ...f, scope: e.target.value }));
              }}
            >
              {scopes.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
            <input type="search" name="name" placeholder="Name contains…" defaultValue={filters.name} />
          </div>
          <div className="row">
            <input type="number" name="min_km" placeholder="Min km" min="0" step="any" />
            <input type="number" name="max_km" placeholder="Max km" min="0" step="any" />
            <select name="sort" defaultValue={filters.sort}>
              <option value="created_at:desc">Newest</option>
              <option value="updated_at:desc">Recently edited</option>
              <option value="distance:desc">Longest</option>
              <option value="distance:asc">Shortest</option>
              <option value="ascent:desc">Most ascent</option>
              <option value="name:asc">Name A–Z</option>
            </select>
          </div>
          <div className="row">
            <label className="check">
              <input type="checkbox" name="in_view" /> Only in map view
            </label>
            <button type="submit">Apply</button>
          </div>
        </form>
      </section>
      <ul className="list">
        {res?.items.map((r) => (
          <RouteItem key={r.id} r={r} selected={r.id === selectedId} onSelect={onSelect} />
        ))}
        {res && res.items.length === 0 && (
          <div className="empty">{user ? "No routes here yet." : "No public routes yet."}</div>
        )}
        {error && <div className="empty msg error">{error}</div>}
      </ul>
      <Pager res={res} onPage={setPage} />
    </div>
  );
}
