"use client";

import { FolderOpen, Route as RouteIcon, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, routesApi } from "@/lib/api";
import { type MapView, useRouteClusters } from "@/lib/route-map";
import { useSession } from "@/lib/session";
import type { Collection, ProfilePoint, Route } from "@/lib/types";
import Map, { previewStyle, routeStyle, type BBox, type MapLayer } from "../Map";
import { cn } from "@/lib/utils";
import { ErrorText, errorMessage } from "../common";
import { Button } from "../ui/button";
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs";
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
  const [view, setView] = useState<MapView | null>(null);
  const bbox = view?.bbox ?? null;
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

  // Every route the user can see, or the open collection's routes.
  const collectionId = tab === "collections" ? collection?.id : undefined;
  const mapFilter = useMemo(() => (collectionId ? { collection_id: collectionId } : {}), [collectionId]);
  const { clusters, truncated } = useRouteClusters(view, mapFilter, reloadKey);
  const onMoveEnd = useCallback((b: BBox, zoom: number) => setView({ bbox: b, zoom }), []);
  const loadClusterRoutes = useCallback(
    (b: BBox) => {
      // A hair larger: the box of identical start points is a single point.
      const pad = 1e-4;
      return routesApi
        .list({
          ...(collectionId ? { collection_id: collectionId } : { scope: user ? "all" : "public" }),
          starts_in: [b[0] - pad, b[1] - pad, b[2] + pad, b[3] + pad].map((n) => n.toFixed(6)).join(","),
          page_size: 50,
          sort: "name",
          order: "asc",
        })
        .then((res) => res.items);
    },
    [collectionId, user],
  );

  const layers = useMemo(() => {
    const l: MapLayer[] = [];
    if (selection) l.push({ key: "route", data: selection.feature, style: routeStyle });
    if (preview) l.push({ key: "preview", data: preview, style: previewStyle });
    return l;
  }, [selection, preview]);

  const panel = "rounded-2xl bg-card/95 shadow-soft ring-1 ring-foreground/8 backdrop-blur-md";
  return (
    <div className="relative flex flex-col gap-3 p-3 lg:h-full lg:p-0">
      {/* The map fills the page on large screens; panels float over it. */}
      <div className="relative order-2 h-[55vh] overflow-hidden rounded-2xl ring-1 ring-foreground/8 lg:absolute lg:inset-0 lg:h-full lg:rounded-none lg:ring-0">
        <Map
          layers={layers}
          fitKey={fitKey}
          onMoveEnd={onMoveEnd}
          clusters={clusters}
          selectedRouteId={selection?.route.id}
          onRouteClick={select}
          loadClusterRoutes={loadClusterRoutes}
        />
        {truncated && (
          <p className="pointer-events-none absolute top-4 left-1/2 z-[500] -translate-x-1/2 rounded-full bg-card/95 px-3 py-1 text-xs text-muted-foreground shadow-soft ring-1 ring-foreground/8 lg:left-[calc(50%+200px)]">
            Showing the largest groups only; zoom in for more.
          </p>
        )}
      </div>
      <aside
        className={cn(
          panel,
          "order-1 flex max-h-[70vh] flex-col overflow-hidden lg:absolute lg:top-4 lg:bottom-4 lg:left-4 lg:z-10 lg:max-h-none lg:w-[380px]",
        )}
      >
        <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)} className="border-b p-3">
          <TabsList className="w-full">
            <TabsTrigger value="routes">
              <RouteIcon /> Routes
            </TabsTrigger>
            <TabsTrigger value="collections">
              <FolderOpen /> Collections
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {!ready ? (
            <div className="grid gap-2">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-14 animate-pulse rounded-xl bg-muted" />
              ))}
            </div>
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
        </div>
      </aside>
      {(error || selection) && (
        <section
          aria-label="Selected route"
          className={cn(
            panel,
            "order-3 overflow-y-auto p-5 lg:absolute lg:right-4 lg:bottom-4 lg:z-10 lg:max-h-[min(62%,640px)] lg:w-[min(540px,calc(100%-420px))]",
          )}
        >
          {error && <ErrorText>{error}</ErrorText>}
          {selection && (
            <>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Close"
                title="Close"
                className="float-right -mt-2 -mr-2 ml-2"
                onClick={deselect}
              >
                <X />
              </Button>
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
            </>
          )}
        </section>
      )}
    </div>
  );
}
