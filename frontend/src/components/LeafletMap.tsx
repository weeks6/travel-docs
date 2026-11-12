"use client";

// Leaflet touches window on import, so this module is only loaded in the
// browser (see Map.tsx).

import L from "leaflet";
import { useEffect, useRef, useState } from "react";

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
  onMoveEnd?: (bbox: BBox) => void;
}

const clamp = (v: number, lim: number) => Math.max(-lim, Math.min(lim, v));
const noLayers: MapLayer[] = [];
const noMarkers: MapMarker[] = [];

export default function LeafletMap({ layers = noLayers, markers = noMarkers, fitKey, onMoveEnd }: MapProps) {
  const el = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<L.Map | null>(null);
  const layerGroup = useRef<L.FeatureGroup | null>(null);
  const markerObjs = useRef(new Map<string, { dot: L.CircleMarker; ring?: L.Circle }>());
  const moveEnd = useRef(onMoveEnd);
  useEffect(() => {
    moveEnd.current = onMoveEnd;
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
    m.on("moveend", () => {
      const b = m.getBounds();
      moveEnd.current?.([
        clamp(b.getWest(), 180),
        clamp(b.getSouth(), 90),
        clamp(b.getEast(), 180),
        clamp(b.getNorth(), 90),
      ]);
    });
    const resize = new ResizeObserver(() => m.invalidateSize());
    resize.observe(node);
    const objs = markerObjs.current;
    // Deferred so that the effect doesn't set state synchronously.
    const t = setTimeout(() => setMap(m));
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
        o.dot.bindTooltip(mk.label, { permanent: true, direction: "right", offset: [10, 0], className: "marker-label" });
        objs.set(mk.id, o);
      } else {
        o.dot.setLatLng([mk.lat, mk.lon]).setStyle(style);
        o.dot.setTooltipContent(mk.label);
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
