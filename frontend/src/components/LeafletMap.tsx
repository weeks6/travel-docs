"use client";

// Leaflet touches window on import, so this module is only loaded in the
// browser (see Map.tsx).

import L from "leaflet";
import { useEffect, useRef, useState } from "react";
import type { MapCluster } from "@/lib/types";

export interface MapLayer {
  key: string;
  data: GeoJSON.GeoJsonObject;
  style?: L.PathOptions;
}

export interface MapMarker {
  id: string;
  lat: number;
  lon: number;
  label: string;
  color: string;
  faded?: boolean; // offline or stale
  accuracy?: number; // meters
}

export type BBox = [number, number, number, number]; // minLon, minLat, maxLon, maxLat

export interface MapProps {
  layers?: MapLayer[];
  markers?: MapMarker[];
  // Changing fitKey fits the view to the layers (and markers).
  fitKey?: string | number;
  // Called with the visible area and zoom level once the map is ready and
  // after every move.
  onMoveEnd?: (bbox: BBox, zoom: number) => void;
  // Route start points, grouped (see GET /routes/map).
  clusters?: MapCluster[];
  selectedRouteId?: string;
  onRouteClick?: (id: string) => void;
  // Lists the routes of a cluster that zooming in can't split.
  loadClusterRoutes?: (bbox: BBox) => Promise<{ id: string; name: string }[]>;
}

// Zooming to a cluster stops here; closer clusters open a list instead.
const MAX_CLUSTER_ZOOM = 18;

const clamp = (v: number, lim: number) => Math.max(-lim, Math.min(lim, v));
const noLayers: MapLayer[] = [];
const noMarkers: MapMarker[] = [];
const noClusters: MapCluster[] = [];

export default function LeafletMap({
  layers = noLayers,
  markers = noMarkers,
  fitKey,
  onMoveEnd,
  clusters = noClusters,
  selectedRouteId,
  onRouteClick,
  loadClusterRoutes,
}: MapProps) {
  const el = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<L.Map | null>(null);
  const layerGroup = useRef<L.FeatureGroup | null>(null);
  const markerObjs = useRef(new Map<string, { dot: L.CircleMarker; ring?: L.Circle }>());
  const moveEnd = useRef(onMoveEnd);
  const routeClick = useRef(onRouteClick);
  const loadRoutes = useRef(loadClusterRoutes);
  useEffect(() => {
    moveEnd.current = onMoveEnd;
    routeClick.current = onRouteClick;
    loadRoutes.current = loadClusterRoutes;
  });

  // Create the map. Effects re-run when a hidden route becomes visible again,
  // so everything below depends on `map`.
  useEffect(() => {
    const node = el.current;
    if (!node) return;
    const m = L.map(node, { worldCopyJump: true, zoomControl: false }).setView([46, 8], 4);
    // Top right: panels float over the left side of the map.
    L.control.zoom({ position: "topright" }).addTo(m);
    // OpenStreetMap's tile policy requires a Referer header.
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
      referrerPolicy: "strict-origin-when-cross-origin",
    }).addTo(m);
    const emitMove = () => {
      const b = m.getBounds();
      moveEnd.current?.(
        [clamp(b.getWest(), 180), clamp(b.getSouth(), 90), clamp(b.getEast(), 180), clamp(b.getNorth(), 90)],
        m.getZoom(),
      );
    };
    m.on("moveend", emitMove);
    const resize = new ResizeObserver(() => m.invalidateSize());
    resize.observe(node);
    const objs = markerObjs.current;
    // Deferred so that the effect doesn't set state synchronously.
    const t = setTimeout(() => {
      setMap(m);
      emitMove(); // the first view
    });
    return () => {
      clearTimeout(t);
      resize.disconnect();
      objs.clear();
      layerGroup.current = null;
      m.remove();
      setMap(null);
    };
  }, []);

  useEffect(() => {
    if (!map) return;
    const group = L.featureGroup();
    for (const l of layers) {
      L.geoJSON(l.data, {
        style: () => l.style || {},
        pointToLayer: (_f, latlng) => L.circleMarker(latlng, { radius: 4, ...(l.style || {}) }),
      }).addTo(group);
    }
    group.addTo(map);
    layerGroup.current = group;
    return () => {
      group.remove();
      if (layerGroup.current === group) layerGroup.current = null;
    };
  }, [map, layers]);

  useEffect(() => {
    if (!map) return;
    const objs = markerObjs.current;
    const seen = new Set<string>();
    for (const mk of markers) {
      seen.add(mk.id);
      const style: L.CircleMarkerOptions = {
        radius: 8,
        color: "#fff",
        weight: 2,
        fillColor: mk.color,
        fillOpacity: mk.faded ? 0.45 : 1,
        opacity: mk.faded ? 0.6 : 1,
      };
      let o = objs.get(mk.id);
      if (!o) {
        o = { dot: L.circleMarker([mk.lat, mk.lon], style).addTo(map) };
        o.dot.bindTooltip(textElement(mk.label), { permanent: true, direction: "right", offset: [10, 0], className: "marker-label" });
        objs.set(mk.id, o);
      } else {
        o.dot.setLatLng([mk.lat, mk.lon]).setStyle(style);
        o.dot.setTooltipContent(textElement(mk.label));
      }
      if (mk.accuracy && mk.accuracy > 15) {
        if (!o.ring) {
          o.ring = L.circle([mk.lat, mk.lon], { radius: mk.accuracy, color: mk.color, weight: 1, fillOpacity: 0.08 }).addTo(map);
        } else {
          o.ring.setLatLng([mk.lat, mk.lon]).setRadius(mk.accuracy);
        }
      } else if (o.ring) {
        o.ring.remove();
        o.ring = undefined;
      }
    }
    for (const [id, o] of objs) {
      if (!seen.has(id)) {
        o.dot.remove();
        o.ring?.remove();
        objs.delete(id);
      }
    }
  }, [map, markers]);

  // Route clusters: a bubble with the count, or a pin for a single route.
  useEffect(() => {
    if (!map) return;
    const group = L.layerGroup();
    for (const c of clusters) {
      if (c.route) {
        const route = c.route;
        const selected = route.id === selectedRouteId;
        L.marker([c.lat, c.lon], {
          icon: L.divIcon({
            className: "",
            html: `<span class="route-pin${selected ? " is-selected" : ""}"></span>`,
            iconSize: [18, 18],
          }),
          keyboard: false,
          zIndexOffset: selected ? 1000 : 0,
        })
          .bindTooltip(textElement(route.name), { direction: "top", offset: [0, -10], className: "marker-label" })
          .on("click", () => routeClick.current?.(route.id))
          .addTo(group);
        continue;
      }
      const size = c.count < 10 ? 32 : c.count < 100 ? 38 : c.count < 1000 ? 44 : 50;
      const label = c.count < 1000 ? String(c.count) : `${Math.floor(c.count / 1000)}k`;
      const marker = L.marker([c.lat, c.lon], {
        icon: L.divIcon({
          className: "",
          html: `<span class="route-cluster" style="width:${size}px;height:${size}px">${label}</span>`,
          iconSize: [size, size],
        }),
        title: `${c.count} routes`,
        keyboard: false,
      });
      marker.on("click", () => {
        const bounds = L.latLngBounds([c.bbox[1], c.bbox[0]], [c.bbox[3], c.bbox[2]]);
        const target = Math.min(map.getBoundsZoom(bounds, false, L.point(80, 80)), MAX_CLUSTER_ZOOM);
        if (target > map.getZoom()) {
          map.flyToBounds(bounds, { padding: [40, 40], maxZoom: MAX_CLUSTER_ZOOM, duration: 0.6 });
          return;
        }
        // The routes start too close together to split: list them.
        const list = document.createElement("div");
        list.className = "cluster-list";
        list.textContent = "Loading…";
        marker.bindPopup(list, { maxWidth: 260 }).openPopup();
        loadRoutes.current?.(c.bbox).then(
          (routes) => {
            list.replaceChildren();
            const title = document.createElement("p");
            title.textContent = `${c.count} routes start here`;
            list.append(title);
            for (const r of routes) {
              const b = document.createElement("button");
              b.type = "button";
              b.textContent = r.name;
              b.title = r.name;
              b.onclick = () => {
                marker.closePopup();
                routeClick.current?.(r.id);
              };
              list.append(b);
            }
          },
          () => (list.textContent = "Could not load the routes."),
        );
      });
      marker.addTo(group);
    }
    group.addTo(map);
    return () => {
      group.remove();
    };
  }, [map, clusters, selectedRouteId]);

  // Fit the view when fitKey changes, after layers and markers are drawn.
  // Layers and markers are read from refs on purpose: their own updates
  // must not move the view.
  useEffect(() => {
    if (!map || fitKey === undefined) return;
    const bounds = L.latLngBounds([]);
    const group = layerGroup.current;
    if (group) {
      const b = group.getBounds();
      if (b.isValid()) bounds.extend(b);
    }
    for (const o of markerObjs.current.values()) bounds.extend(o.dot.getLatLng());
    if (!bounds.isValid()) return;
    if (bounds.getNorthEast().equals(bounds.getSouthWest())) map.setView(bounds.getCenter(), 15);
    else map.fitBounds(bounds, { padding: [32, 32], maxZoom: 16 });
  }, [map, fitKey]);

  return <div ref={el} className="isolate size-full min-h-64" />;
}

// Leaflet inserts strings as HTML; user-provided text goes in as text.
function textElement(text: string): HTMLElement {
  const el = document.createElement("span");
  el.textContent = text;
  return el;
}
