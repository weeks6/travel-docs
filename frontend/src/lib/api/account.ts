import type { AuditEvent, ImportJob, Page, Session, User } from "../types";
import { api, upload } from "./client";
import { query, seg } from "./query";

export const authApi = {
  login: (fields: Record<string, unknown>) => api<Session>("/auth/login", { method: "POST", json: fields }),
  register: (fields: Record<string, unknown>) => api<Session>("/auth/register", { method: "POST", json: fields }),
  updateMe: (fields: Record<string, unknown>) => api<User>("/auth/me", { method: "PATCH", json: fields }),
  /** Changes the password; other sessions end and a new session is returned. */
  changePassword: (fields: Record<string, unknown>) =>
    api<Session>("/auth/me/password", { method: "PUT", json: fields }),
  logoutAll: () => api<null>("/auth/logout-all", { method: "POST" }),
  deleteMe: (fields: Record<string, unknown>) => api<null>("/auth/me", { method: "DELETE", json: fields }),
};

export const auditApi = {
  /** The user's activity, newest first; pass next_before_id to load older events. */
  list: (beforeId?: number, limit = 30) =>
    api<{ items: AuditEvent[]; next_before_id?: number }>(
      "/audit-events" + query({ limit, before_id: beforeId || undefined }),
    ),
};

const j = (id: string) => `/import-jobs/${seg(id)}`;

export const importsApi = {
  list: (q: { page?: number; page_size?: number; status?: string /* active, finished; "" = all */ } = {}) =>
    api<Page<ImportJob>>("/import-jobs" + query(q)),
  get: (id: string) => api<ImportJob>(j(id)),
  /** Queues a GPX file (multipart form with "file" and optional fields) with upload progress. */
  upload: (form: FormData, opts: Parameters<typeof upload>[2]) => upload<ImportJob>("/import-jobs", form, opts),
  cancel: (id: string) => api<ImportJob>(j(id) + "/cancel", { method: "POST" }),
  retry: (id: string) => api<ImportJob>(j(id) + "/retry", { method: "POST" }),
  remove: (id: string) => api<null>(j(id), { method: "DELETE" }),
};

