"use client";

import { Globe, Route as RouteIcon, Search, SlidersHorizontal, TrendingUp } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type RouteQuery, routesApi } from "@/lib/api";
import { date, km, meters } from "@/lib/format";
import type { Page, Route, User } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import { ImportForm } from "../ImportForm";
import { type BBox } from "../Map";
import { cn } from "@/lib/utils";
import { EmptyState, ErrorText, Pager, RoleBadge, errorMessage } from "../common";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { NativeSelect, NativeSelectOption } from "../ui/native-select";
import { Separator } from "../ui/separator";

export function RouteItem({ r, selected, onSelect }: { r: Route; selected: boolean; onSelect: (r: Route) => void }) {
  return (
    <li>
      <button
        type="button"
        aria-current={selected ? "true" : undefined}
        onClick={() => onSelect(r)}
        className={cn(
          "group flex w-full items-start gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-muted/70",
          selected && "bg-accent ring-1 ring-primary/25 hover:bg-accent",
        )}
      >
        <span
          className={cn(
            "mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg bg-secondary text-muted-foreground transition-colors",
            selected && "bg-primary text-primary-foreground",
          )}
        >
          <RouteIcon className="size-4" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <span className="truncate text-sm font-semibold">{r.name}</span>
            {r.visibility === "public" && <Globe className="size-3.5 shrink-0 text-success" aria-label="public" />}
            {r.my_role !== "owner" && <RoleBadge role={r.my_role} />}
          </span>
          <span className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-muted-foreground tabular-nums">
            <span>{km(r.distance_m)}</span>
            <span className="inline-flex items-center gap-0.5">
              <TrendingUp className="size-3" aria-hidden /> {meters(r.ascent_m)}
            </span>
            <span>{date(r.started_at || r.created_at)}</span>
            {r.my_role !== "owner" && <span className="truncate">by {r.owner_name}</span>}
          </span>
        </span>
      </button>
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
    <div className="grid gap-4">
      {user ? (
        <section className="grid gap-2">
          <h2 className="text-sm font-semibold">Add a route</h2>
          <ImportForm onSucceeded={(job) => onImported(job.route_id)} />
        </section>
      ) : (
        <LoginPrompt title="Bring your own tracks" text="Log in to upload GPX files and keep a log of your trips." />
      )}
      <Separator />
      <form
        className="grid gap-2"
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
        <div className="flex gap-2">
          <NativeSelect
            aria-label="Show"
            value={filters.scope}
            className="w-36 shrink-0"
            onChange={(e) => {
              setPage(1);
              setFilters((f) => ({ ...f, scope: e.target.value }));
            }}
          >
            {scopes.map(([v, l]) => (
              <NativeSelectOption key={v} value={v}>
                {l}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input type="search" name="name" placeholder="Search by name" aria-label="Name contains" defaultValue={filters.name} className="pl-8" />
          </div>
        </div>
        <div className="flex gap-2">
          <Input type="number" name="min_km" placeholder="Min km" aria-label="Min km" min="0" step="any" className="w-0 flex-1" />
          <Input type="number" name="max_km" placeholder="Max km" aria-label="Max km" min="0" step="any" className="w-0 flex-1" />
          <NativeSelect name="sort" aria-label="Sort" defaultValue={filters.sort} className="flex-[1.4]">
            <NativeSelectOption value="created_at:desc">Newest</NativeSelectOption>
            <NativeSelectOption value="updated_at:desc">Recently edited</NativeSelectOption>
            <NativeSelectOption value="distance:desc">Longest</NativeSelectOption>
            <NativeSelectOption value="distance:asc">Shortest</NativeSelectOption>
            <NativeSelectOption value="ascent:desc">Most ascent</NativeSelectOption>
            <NativeSelectOption value="name:asc">Name A–Z</NativeSelectOption>
          </NativeSelect>
        </div>
        <div className="flex items-center justify-between gap-2">
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input type="checkbox" name="in_view" className="size-4 accent-primary" /> Only in map view
          </label>
          <Button type="submit" variant="secondary" size="sm">
            <SlidersHorizontal data-icon="inline-start" /> Apply
          </Button>
        </div>
      </form>
      <div className="grid gap-1">
        {res && (
          <p className="px-1 text-xs text-muted-foreground">
            {res.total} {res.total === 1 ? "route" : "routes"}
          </p>
        )}
        <ul className="grid gap-0.5">
          {res?.items.map((r) => (
            <RouteItem key={r.id} r={r} selected={r.id === selectedId} onSelect={onSelect} />
          ))}
          {!res && !error && [0, 1, 2].map((i) => <li key={i} className="h-14 animate-pulse rounded-xl bg-muted" />)}
        </ul>
        {res && res.items.length === 0 && (
          <EmptyState icon={<RouteIcon className="size-5" />} title={user ? "No routes here yet" : "No public routes yet"}>
            {user ? "Upload a GPX file above to add your first route." : undefined}
          </EmptyState>
        )}
        {error && <ErrorText>{error}</ErrorText>}
      </div>
      <Pager res={res} onPage={setPage} />
    </div>
  );
}
