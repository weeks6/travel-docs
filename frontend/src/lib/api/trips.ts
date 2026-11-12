import type { Invitation, Page, Participant, Trip } from "../types";
import { api } from "./client";
import { query, seg } from "./query";

const t = (id: string) => `/trips/${seg(id)}`;

export const tripsApi = {
  list: (q: { page?: number; page_size?: number; status?: string /* planned, active, ended; "" = all */ } = {}) =>
    api<Page<Trip>>("/trips" + query(q)),
  get: (id: string) => api<Trip>(t(id)),
  create: (fields: Record<string, unknown>) => api<Trip>("/trips", { method: "POST", json: fields }),
  update: (id: string, fields: Record<string, unknown>) => api<Trip>(t(id), { method: "PATCH", json: fields }),
  remove: (id: string) => api<null>(t(id), { method: "DELETE" }),
  start: (id: string) => api<Trip>(t(id) + "/start", { method: "POST" }),
  end: (id: string) => api<Trip>(t(id) + "/end", { method: "POST" }),
  participants: (id: string) => api<{ items: Participant[] }>(t(id) + "/participants"),
  /** Removes a participant; with your own ID, leaves the trip. */
  removeParticipant: (id: string, userId: string) =>
    api<null>(`${t(id)}/participants/${seg(userId)}`, { method: "DELETE" }),
  /** The location history as one feature per participant. */
  history: (id: string) => api<GeoJSON.FeatureCollection>(t(id) + "/history"),
  invitations: (id: string) => api<{ items: Invitation[] }>(t(id) + "/invitations"),
  invite: (id: string, email: string) =>
    api<Invitation>(t(id) + "/invitations", { method: "POST", json: { email } }),
  revokeInvitation: (id: string, invitationId: string) =>
    api<null>(`${t(id)}/invitations/${seg(invitationId)}`, { method: "DELETE" }),
};

export const invitationsApi = {
  /** The current user's invitations (pending ones by default). */
  mine: () => api<{ items: Invitation[] }>("/trip-invitations"),
  accept: (id: string) => api<Invitation>(`/trip-invitations/${seg(id)}/accept`, { method: "POST" }),
  decline: (id: string) => api<Invitation>(`/trip-invitations/${seg(id)}/decline`, { method: "POST" }),
};
