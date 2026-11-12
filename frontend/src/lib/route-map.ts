"use client";

import { useEffect, useRef, useState } from "react";
import { routesApi } from "./api";
import { type BBox, contains, tileArea } from "./map-area";
import type { MapCluster } from "./types";

export interface MapView {
  bbox: BBox;
  zoom: number;
}

/** What the map shows besides the view: a collection, or everything. */
export interface MapFilter {
  collection_id?: string;
}

const DEBOUNCE_MS = 250;
const CACHE_SIZE = 24;
// After this long, an answer is fetched again, so routes others added show up.
const MAX_AGE_MS = 2 * 60_000;

/**
 * Loads the route clusters for the map view. To keep requests few and
 * small:
 *
 * - the API clusters on the server, so a response has at most a few hundred
 *   features at any zoom;
 * - the area requested is the view grown to whole map tiles plus one tile
 *   around it, so panning within it needs no request, and the clusters at
 *   the edge of the view are never cut off;
 * - moves are debounced, and a newer view aborts an older request;
 * - recent answers are cached for MAX_AGE_MS (cleared when reloadKey
 *   changes, e.g. after an import), so zooming back and forth is instant.
 */
export function useRouteClusters(view: MapView | null, filter: MapFilter, reloadKey: number) {
  const [clusters, setClusters] = useState<MapCluster[]>([]);
  const [truncated, setTruncated] = useState(false);
  const cache = useRef(new Map<string, { area: BBox; clusters: MapCluster[]; truncated: boolean; at: number }>());
  const loaded = useRef<{ area: BBox; zoom: number; filterKey: string; at: number } | null>(null);
  const filterKey = JSON.stringify(filter);

  useEffect(() => {
    cache.current.clear();
    loaded.current = null;
  }, [reloadKey, filterKey]);

  useEffect(() => {
    if (!view) return;
    const zoom = Math.round(view.zoom);
    // Still inside what was loaded for this zoom: nothing to do.
    const l = loaded.current;
    const now = Date.now();
    if (l && l.zoom === zoom && l.filterKey === filterKey && contains(l.area, view.bbox) && now - l.at < MAX_AGE_MS) return;

    const area = tileArea(view.bbox, zoom);
    const key = `${filterKey}|${zoom}|${area.join(",")}`;
    let hit = cache.current.get(key);
    if (hit && now - hit.at >= MAX_AGE_MS) {
      cache.current.delete(key);
      hit = undefined;
    }
    const ctl = new AbortController();
    // Timers also keep the effect from setting state synchronously.
    const t = setTimeout(() => {
      if (hit) {
        cache.current.delete(key); // move to the end: most recently used
        cache.current.set(key, hit);
        loaded.current = { area, zoom, filterKey, at: hit.at };
        setClusters(hit.clusters);
        setTruncated(hit.truncated);
        return;
      }
      routesApi
        .map({ ...JSON.parse(filterKey), bbox: area.map((n) => +n.toFixed(6)).join(","), zoom }, ctl.signal)
        .then((res) => {
          const at = Date.now();
          cache.current.set(key, { area, clusters: res.clusters, truncated: res.truncated, at });
          if (cache.current.size > CACHE_SIZE) cache.current.delete(cache.current.keys().next().value!);
          loaded.current = { area, zoom, filterKey, at };
          setClusters(res.clusters);
          setTruncated(res.truncated);
        })
        .catch(() => {
          // Aborted, or failed: keep showing what we have; the next move retries.
        });
    }, hit ? 0 : DEBOUNCE_MS);
    return () => {
      clearTimeout(t);
      ctl.abort();
    };
  }, [view, filterKey, reloadKey]);

  return { clusters, truncated };
}
