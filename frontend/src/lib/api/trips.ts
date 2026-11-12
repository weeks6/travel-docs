import type { Page, Trip, TripStats } from "../types";
import { api } from "./client";
import { query, seg } from "./query";

const t = (id: string) => `/trips/${seg(id)}`;

/** Filters of GET /trips and /trips/stats. */
export interface TripFilter {
  year?: number;
  route_id?: string;
}

/** Fields of a trip; omitted ones default to the route's recording. */
export interface TripFields {
  name?: string;
  notes?: string;
  date?: string; // YYYY-MM-DD
  duration_s?: number;
}

export const tripsApi = {
  list: (q: TripFilter & { page?: number; page_size?: number } = {}) => api<Page<Trip>>("/trips" + query({ ...q })),
  stats: (q: TripFilter = {}) => api<TripStats>("/trips/stats" + query({ ...q })),
  get: (id: string) => api<Trip>(t(id)),
  /** Logs a trip on a route you can see. */
  create: (routeId: string, fields: TripFields) =>
    api<Trip>("/trips", { method: "POST", json: { route_id: routeId, ...fields } }),
  /** Logs a trip with a GPX file ("file" plus TripFields and "visibility"); the file becomes a new route. */
  upload: (form: FormData) => api<Trip>("/trips", { method: "POST", body: form }),
  update: (id: string, fields: TripFields) => api<Trip>(t(id), { method: "PATCH", json: fields }),
  remove: (id: string) => api<null>(t(id), { method: "DELETE" }),
};
