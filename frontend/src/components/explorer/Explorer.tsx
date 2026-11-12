"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, routesApi } from "@/lib/api";
import { useSession } from "@/lib/session";
import type { Collection, ProfilePoint, Route } from "@/lib/types";
import Map, { type BBox, type MapLayer } from "../Map";
import { errorMessage } from "../ui";
import { RouteDetails } from "./RouteDetails";
import { RoutesPanel } from "./RoutesPanel";
import { CollectionsPanel } from "./Collections";

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
      routesApi.get(id),
      routesApi.geometry(id),
      routesApi.elevation(id),
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
