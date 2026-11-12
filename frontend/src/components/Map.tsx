"use client";

import type { PathOptions } from "leaflet";
import dynamic from "next/dynamic";

// Leaflet needs the browser, so the map is never rendered on the server.
const Map = dynamic(() => import("./LeafletMap"), {
  ssr: false,
  loading: () => (
    <div className="topo grid size-full min-h-64 place-items-center bg-muted text-sm text-muted-foreground">Loading map…</div>
  ),
});

export default Map;
export type { BBox, MapLayer, MapMarker } from "./LeafletMap";

// Line styles in the theme's colors (Leaflet can't read CSS variables, so
// these are the theme's rose and sand colors, readable on light and dark tiles). Here rather than in
// LeafletMap so that importing them doesn't load Leaflet on the server.
export const routeStyle: PathOptions = { color: "#9a3e67", weight: 5, opacity: 0.95, lineCap: "round", lineJoin: "round" };
export const previewStyle: PathOptions = { color: "#d09033", weight: 4, dashArray: "2 9", lineCap: "round" };
