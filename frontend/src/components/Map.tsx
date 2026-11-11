"use client";

import dynamic from "next/dynamic";

// Leaflet needs the browser, so the map is never rendered on the server.
const Map = dynamic(() => import("./LeafletMap"), {
  ssr: false,
  loading: () => <div className="map map-loading">Loading map…</div>,
});

export default Map;
export type { BBox, MapLayer, MapMarker } from "./LeafletMap";
