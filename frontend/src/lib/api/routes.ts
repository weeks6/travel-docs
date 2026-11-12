import type { Collection, Page, ProfilePoint, Route, RouteVersion, Share } from "../types";
import { api } from "./client";
import { ifMatch, query, seg, type QueryValue } from "./query";

/** Filters of GET /routes (see the API README). */
export interface RouteQuery {
  page?: number;
  page_size?: number;
  scope?: string; // mine, shared, public or all
  collection_id?: string;
  name?: string;
  min_distance_m?: number;
  max_distance_m?: number;
  min_ascent_m?: number;
  max_ascent_m?: number;
  bbox?: string; // minLon,minLat,maxLon,maxLat
  sort?: string;
  order?: string;
}

const r = (id: string) => `/routes/${seg(id)}`;

export const routesApi = {
  list: (q: RouteQuery = {}) => api<Page<Route>>("/routes" + query(q as Record<string, QueryValue>)),
  get: (id: string) => api<Route>(r(id)),
  geometry: (id: string) => api<GeoJSON.Feature>(r(id) + "/geometry"),
  elevation: (id: string) => api<{ points: ProfilePoint[] }>(r(id) + "/elevation"),
  /** Changes name/description/visibility; fails with 412 if the route is no longer at version. */
  update: (id: string, version: number, fields: Record<string, unknown>) =>
    api<Route>(r(id), { method: "PATCH", json: fields, headers: ifMatch(version) }),
  remove: (id: string) => api<null>(r(id), { method: "DELETE" }),
  versions: (id: string) => api<{ items: RouteVersion[] }>(r(id) + "/versions"),
  versionGeometry: (id: string, version: number) =>
    api<GeoJSON.Feature>(`${r(id)}/versions/${seg(version)}/geometry`),
  /** Makes an old version current; fails with 412 if the route is no longer at currentVersion. */
  restore: (id: string, version: number, currentVersion: number) =>
    api<Route>(`${r(id)}/versions/${seg(version)}/restore`, { method: "POST", headers: ifMatch(currentVersion) }),
};

/** Filters of GET /collections. */
export interface CollectionQuery {
  page?: number;
  page_size?: number;
  scope?: string; // mine, shared, editable, public or all
  route_id?: string;
}

const c = (id: string) => `/collections/${seg(id)}`;

export const collectionsApi = {
  list: (q: CollectionQuery = {}) => api<Page<Collection>>("/collections" + query(q as Record<string, QueryValue>)),
  get: (id: string) => api<Collection>(c(id)),
  create: (fields: Record<string, unknown>) => api<Collection>("/collections", { method: "POST", json: fields }),
  update: (id: string, fields: Record<string, unknown>) => api<Collection>(c(id), { method: "PATCH", json: fields }),
  remove: (id: string) => api<null>(c(id), { method: "DELETE" }),
  addRoute: (id: string, routeId: string) => api<null>(`${c(id)}/routes/${seg(routeId)}`, { method: "PUT" }),
  removeRoute: (id: string, routeId: string) => api<null>(`${c(id)}/routes/${seg(routeId)}`, { method: "DELETE" }),
};

/** Shares of a route or collection (owner only). */
export function sharesApi(kind: "route" | "collection", id: string) {
  const base = (kind === "route" ? r(id) : c(id)) + "/shares";
  return {
    list: () => api<{ items: Share[] }>(base),
    put: (fields: Record<string, unknown>) => api<Share>(base, { method: "PUT", json: fields }),
    remove: (userId: string) => api<null>(`${base}/${seg(userId)}`, { method: "DELETE" }),
  };
}
