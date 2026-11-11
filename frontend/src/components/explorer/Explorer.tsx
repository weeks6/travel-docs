"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, api } from "@/lib/api";
import { date, km, meters, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { Collection, Page, ProfilePoint, Route, User } from "@/lib/types";
import { LoginPrompt } from "../Header";
import { ImportForm } from "../ImportForm";
import Map, { type BBox, type MapLayer } from "../Map";
import { SharesDialog } from "../SharesDialog";
import { ConfirmButton, Field, Modal, Msg, Pager, RoleBadge, VisibilityBadge, errorMessage, useAction } from "../ui";
import { RouteDetails } from "./RouteDetails";

interface Selection {
  route: Route;
  feature: GeoJSON.Feature;
  profile: ProfilePoint[];
}

export function Explorer() {
  const { user, ready } = useSession();
  const [tab, setTab] = useState<"routes" | "collections">("routes");
  const [selection, setSelection] = useState<Selection | null>(null);
  const [preview, setPreview] = useState<GeoJSON.Feature | null>(null);
  const [fitKey, setFitKey] = useState(0);
  const [reloadKey, setReloadKey] = useState(0);
  const [collection, setCollection] = useState<Collection | null>(null);
  const [bbox, setBbox] = useState<BBox | null>(null);
  const [error, setError] = useState("");
  const request = useRef(0);

  const reloadLists = () => setReloadKey((k) => k + 1);
  const deselect = useCallback(() => {
    request.current++;
    setSelection(null);
    setPreview(null);
  }, []);

  // Loads a route with its geometry and elevation profile.
  // Only the latest request is applied; state changes happen once it answers.
  const select = useCallback((id: string, fit = true) => {
    const n = ++request.current;
    Promise.all([
      api<Route>(`/routes/${id}`),
      api<GeoJSON.Feature>(`/routes/${id}/geometry`),
      api<{ points: ProfilePoint[] }>(`/routes/${id}/elevation`),
    ]).then(
      ([route, feature, elevation]) => {
        if (n !== request.current) return;
        setSelection({ route, feature, profile: elevation.points });
        setPreview(null);
        setError("");
        if (fit) setFitKey((k) => k + 1);
      },
      (e) => {
        if (n !== request.current) return;
        setSelection(null);
        if (e instanceof ApiError && e.status === 404) {
          setError("That route no longer exists or you can't see it.");
          setReloadKey((k) => k + 1);
        } else setError(errorMessage(e));
      },
    );
  }, []);

  // Logging in or out starts over. ?route=<id> opens a route.
  const userID = user?.id ?? null;
  const [prevUser, setPrevUser] = useState<string | null | undefined>(undefined);
  if (ready && prevUser !== userID) {
    setPrevUser(userID);
    setSelection(null);
    setPreview(null);
    setCollection(null);
  }
  useEffect(() => {
    if (!ready) return;
    const id = new URLSearchParams(window.location.search).get("route");
    if (id) select(id);
  }, [ready, userID, select]);

  const layers = useMemo(() => {
    const l: MapLayer[] = [];
    if (selection) l.push({ key: "route", data: selection.feature, style: { color: "#2563eb", weight: 4 } });
    if (preview) l.push({ key: "preview", data: preview, style: { color: "#ea580c", weight: 3, dashArray: "6 6" } });
    return l;
  }, [selection, preview]);

  return (
    <div className="app">
      <aside>
        <nav className="tabs">
          <button type="button" className={tab === "routes" ? "active" : ""} onClick={() => setTab("routes")}>
            Routes
          </button>
          <button
            type="button"
            className={tab === "collections" ? "active" : ""}
            onClick={() => setTab("collections")}
          >
            Collections
          </button>
        </nav>
        {!ready ? (
          <div className="empty">Loading…</div>
        ) : tab === "routes" ? (
          <RoutesPanel
            key={userID ?? "anon"}
            user={user}
            selectedId={selection?.route.id}
            onSelect={(r) => select(r.id)}
            reloadKey={reloadKey}
            bbox={bbox}
            onImported={(routeId) => {
              reloadLists();
              if (routeId) select(routeId);
            }}
          />
        ) : (
          <CollectionsPanel
            key={userID ?? "anon"}
            user={user}
            collection={collection}
            onOpen={(c) => {
              setCollection(c);
              deselect();
            }}
            selectedId={selection?.route.id}
            onSelect={(r) => select(r.id)}
            reloadKey={reloadKey}
          />
        )}
      </aside>
      <main>
        <Map layers={layers} fitKey={fitKey} onMoveEnd={setBbox} />
        {error && <div className="details msg error">{error}</div>}
        {selection && (
          <RouteDetails
            key={selection.route.id}
            route={selection.route}
            profile={selection.profile}
            user={user}
            collection={tab === "collections" ? collection : null}
            onChanged={(updated) => {
              select(updated ? updated.id : selection.route.id, false);
              reloadLists();
            }}
            onDeleted={() => {
              deselect();
              reloadLists();
            }}
            onRemovedFromCollection={() => {
              deselect();
              reloadLists();
            }}
            onPreview={(f) => {
              setPreview(f);
              setFitKey((k) => k + 1);
            }}
          />
        )}
      </main>
    </div>
  );
}

function RouteItem({ r, selected, onSelect }: { r: Route; selected: boolean; onSelect: (r: Route) => void }) {
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

interface Filters {
  scope: string;
  name: string;
  minKm: string;
  maxKm: string;
  sort: string;
  inView: boolean;
}

function RoutesPanel({
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

  const query = useMemo(() => {
    const q = new URLSearchParams({ page: String(page), page_size: "20", scope: filters.scope });
    const [sort, order] = filters.sort.split(":");
    q.set("sort", sort);
    q.set("order", order);
    if (filters.name) q.set("name", filters.name);
    if (filters.minKm) q.set("min_distance_m", String(Number(filters.minKm) * 1000));
    if (filters.maxKm) q.set("max_distance_m", String(Number(filters.maxKm) * 1000));
    if (filters.inView && bbox) q.set("bbox", bbox.join(","));
    return q.toString();
  }, [page, filters, bbox]);

  useEffect(() => {
    let active = true;
    api<Page<Route>>("/routes?" + query)
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

function CollectionsPanel({
  user,
  collection,
  onOpen,
  selectedId,
  onSelect,
  reloadKey,
}: {
  user: User | null;
  collection: Collection | null;
  onOpen: (c: Collection | null) => void;
  selectedId?: string;
  onSelect: (r: Route) => void;
  reloadKey: number;
}) {
  const [scope, setScope] = useState(user ? "mine" : "public");
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Collection> | null>(null);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (collection) return;
    let active = true;
    api<Page<Collection>>(`/collections?scope=${scope}&page=${page}`)
      .then((r) => {
        if (!active) return;
        setRes(r);
        setError("");
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [scope, page, reloadKey, collection]);

  if (collection) {
    return (
      <CollectionView
        key={collection.id}
        initial={collection}
        onBack={() => onOpen(null)}
        selectedId={selectedId}
        onSelect={onSelect}
        reloadKey={reloadKey}
      />
    );
  }

  const scopes = user
    ? [
        ["mine", "My collections"],
        ["shared", "Shared with me"],
        ["public", "Public collections"],
      ]
    : [["public", "Public collections"]];
  return (
    <div className="panel">
      <section>
        <div className="row">
          <select
            value={scope}
            onChange={(e) => {
              setPage(1);
              setScope(e.target.value);
            }}
          >
            {scopes.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
          {user && (
            <button type="button" className="primary" onClick={() => setCreating(true)}>
              New collection
            </button>
          )}
        </div>
      </section>
      <ul className="list">
        {res?.items.map((c) => (
          <li key={c.id} onClick={() => onOpen(c)}>
            <div className="name">
              {c.name}
              {c.visibility === "public" && <span className="badge public">public</span>}
              {c.my_role !== "owner" && <RoleBadge role={c.my_role} />}
            </div>
            <div className="meta">
              {plural(c.route_count, "route")}
              {c.my_role !== "owner" && ` · by ${c.owner_name}`} · updated {date(c.updated_at)}
            </div>
          </li>
        ))}
        {res && res.items.length === 0 && <div className="empty">No collections here yet.</div>}
        {error && <div className="empty msg error">{error}</div>}
      </ul>
      <Pager res={res} onPage={setPage} />
      {creating && (
        <CollectionFormDialog
          collection={null}
          onClose={() => setCreating(false)}
          onSaved={(c) => {
            setCreating(false);
            onOpen(c);
          }}
        />
      )}
    </div>
  );
}

function CollectionView({
  initial,
  onBack,
  selectedId,
  onSelect,
  reloadKey,
}: {
  initial: Collection;
  onBack: () => void;
  selectedId?: string;
  onSelect: (r: Route) => void;
  reloadKey: number;
}) {
  const [c, setC] = useState(initial);
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Route> | null>(null);
  const [version, setVersion] = useState(0);
  const [dialog, setDialog] = useState<null | "edit" | "share">(null);
  const [error, setError] = useState("");
  const back = useRef(onBack);
  useEffect(() => {
    back.current = onBack;
  });

  useEffect(() => {
    let active = true;
    Promise.all([
      api<Collection>(`/collections/${initial.id}`),
      api<Page<Route>>(`/routes?collection_id=${initial.id}&page=${page}&page_size=20`),
    ])
      .then(([col, routes]) => {
        if (!active) return;
        setC(col);
        setRes(routes);
      })
      .catch((e) => {
        if (!active) return;
        if (e instanceof ApiError && e.status === 404) back.current();
        else setError(errorMessage(e));
      });
    return () => {
      active = false;
    };
  }, [initial.id, page, reloadKey, version]);

  const canEdit = c.my_role === "owner" || c.my_role === "editor";
  return (
    <div className="panel">
      <section>
        <div className="collection-title">
          <button type="button" className="link" onClick={onBack}>
            ‹ All
          </button>
          <h3>{c.name}</h3>
          <VisibilityBadge visibility={c.visibility} />
        </div>
        <div className="meta">{c.my_role === "owner" ? "Your collection" : `By ${c.owner_name} · you are ${c.my_role}`}</div>
        {c.description && <p className="description">{c.description}</p>}
        <div className="actions">
          {canEdit && (
            <button type="button" onClick={() => setDialog("edit")}>
              Edit
            </button>
          )}
          {c.my_role === "owner" && (
            <button type="button" onClick={() => setDialog("share")}>
              Share
            </button>
          )}
          {c.my_role === "owner" && (
            <ConfirmButton
              label="Delete"
              className="danger"
              question={`Delete collection “${c.name}”? Its routes are kept.`}
              onConfirm={async () => {
                await api(`/collections/${c.id}`, { method: "DELETE" });
                onBack();
              }}
            />
          )}
        </div>
      </section>
      <ul className="list">
        {res?.items.map((r) => (
          <RouteItem key={r.id} r={r} selected={r.id === selectedId} onSelect={onSelect} />
        ))}
        {res && res.items.length === 0 && (
          <div className="empty">No routes in this collection yet. Select a route and use “Add to collection”.</div>
        )}
        {error && <div className="empty msg error">{error}</div>}
      </ul>
      <Pager res={res} onPage={setPage} />
      {dialog === "edit" && (
        <CollectionFormDialog
          collection={c}
          onClose={() => setDialog(null)}
          onSaved={() => {
            setDialog(null);
            setVersion((v) => v + 1);
          }}
        />
      )}
      {dialog === "share" && <SharesDialog kind="collection" item={c} onClose={() => setDialog(null)} />}
    </div>
  );
}

function CollectionFormDialog({
  collection: c,
  onClose,
  onSaved,
}: {
  collection: Collection | null;
  onClose: () => void;
  onSaved: (c: Collection) => void;
}) {
  const { pending, msg, run } = useAction();
  return (
    <Modal title={c ? "Edit collection" : "New collection"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          run(async () => {
            onSaved(
              c
                ? await api<Collection>(`/collections/${c.id}`, { method: "PATCH", json: fd })
                : await api<Collection>("/collections", { method: "POST", json: fd }),
            );
          });
        }}
      >
        <Field label="Name">
          <input name="name" defaultValue={c?.name || ""} required maxLength={200} />
        </Field>
        <Field label="Description">
          <textarea name="description" defaultValue={c?.description || ""} maxLength={5000} />
        </Field>
        {(!c || c.my_role === "owner") && (
          <Field label="Visibility">
            <select name="visibility" defaultValue={c?.visibility || "private"}>
              <option value="private">Private</option>
              <option value="public">Public</option>
            </select>
          </Field>
        )}
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {c ? "Save" : "Create"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
