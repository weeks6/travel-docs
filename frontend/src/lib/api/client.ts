// API client with automatic token handling.
//
// The access token lives only in memory. The refresh token is kept in
// localStorage so the session survives page reloads; it is sent in the body
// of /auth/refresh and /auth/logout (the API uses no cookies). Every refresh
// rotates the refresh token, so concurrent refreshes share one request.

import { API_URL } from "../config";
import type { Session, User } from "../types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public headers?: Headers,
  ) {
    super(message);
  }
}

const REFRESH_KEY = "travel-docs.refresh_token";
// Refresh the access token when it expires within this many milliseconds.
const REFRESH_MARGIN = 60_000;

let accessToken: string | null = null;
let accessExpiresAt = 0;
let currentUser: User | null = null;
let refreshPromise: Promise<boolean> | null = null;
let initPromise: Promise<void> | null = null;
const listeners = new Set<(user: User | null) => void>();

function storedRefreshToken(): string | null {
  try {
    return localStorage.getItem(REFRESH_KEY);
  } catch {
    return null;
  }
}

function storeRefreshToken(token: string | null) {
  try {
    if (token) localStorage.setItem(REFRESH_KEY, token);
    else localStorage.removeItem(REFRESH_KEY);
  } catch {
    // Storage unavailable: the session lasts until reload.
  }
}

function emit() {
  for (const fn of listeners) fn(currentUser);
}

/** Subscribes to login, logout and profile changes. */
export function onSessionChange(fn: (user: User | null) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function currentSessionUser(): User | null {
  return currentUser;
}

/** Stores a session returned by login, register, refresh or password change. */
export function setSession(s: Session | null) {
  accessToken = s ? s.access_token : null;
  accessExpiresAt = s ? Date.now() + s.expires_in * 1000 : 0;
  currentUser = s ? s.user : null;
  storeRefreshToken(s ? s.refresh_token : null);
  emit();
}

/** Updates the profile after an edit. */
export function setUser(user: User) {
  currentUser = user;
  emit();
}

/**
 * Restores the session from the stored refresh token. Runs once per page
 * load; later calls return the same promise.
 */
export function initSession(): Promise<void> {
  if (!initPromise) {
    initPromise = refreshSession().then(() => undefined);
    // Another tab logged out: follow it.
    try {
      window.addEventListener("storage", (e) => {
        if (e.key === REFRESH_KEY && e.newValue === null && currentUser) {
          accessToken = null;
          currentUser = null;
          emit();
        }
      });
    } catch {
      // not in a browser
    }
  }
  return initPromise;
}

/** Exchanges the refresh token for a new session. */
export function refreshSession(): Promise<boolean> {
  if (refreshPromise) return refreshPromise;
  const token = storedRefreshToken();
  if (!token) {
    if (currentUser) setSession(null);
    return Promise.resolve(false);
  }
  refreshPromise = fetch(API_URL + "/auth/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: token }),
  })
    .then(async (res) => {
      if (res.ok) {
        setSession((await res.json()) as Session);
        return true;
      }
      if (res.status === 429) return false; // keep the session; try again later
      // Another tab may have rotated the token meanwhile; only log out if
      // the stored token is still the one that was rejected.
      if (storedRefreshToken() === token) {
        setSession(null);
        return false;
      }
      refreshPromise = null;
      return refreshSession();
    })
    .catch(() => false)
    .finally(() => {
      refreshPromise = null;
    });
  return refreshPromise;
}

/** Returns a valid access token (refreshing it if needed), or null when logged out. */
export async function getAccessToken(): Promise<string | null> {
  if (accessToken && Date.now() < accessExpiresAt - REFRESH_MARGIN) return accessToken;
  if (storedRefreshToken()) await refreshSession();
  return accessToken;
}

/** When the current access token expires (ms since epoch), or 0. */
export function accessTokenExpiry(): number {
  return accessToken ? accessExpiresAt : 0;
}

export interface RequestOptions {
  method?: string;
  json?: unknown;
  body?: BodyInit;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

/** Sends a request with the access token, refreshing and retrying once on 401. */
export async function apiFetch(path: string, opts: RequestOptions = {}, retried = false): Promise<Response> {
  const headers: Record<string, string> = { ...(opts.headers || {}) };
  let body = opts.body;
  if (opts.json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.json);
  }
  const token = await getAccessToken();
  if (token) headers.Authorization = "Bearer " + token;
  const res = await fetch(API_URL + path, { method: opts.method || "GET", headers, body, signal: opts.signal });
  if (res.status === 401 && token && !retried && (await refreshSession())) {
    return apiFetch(path, opts, true);
  }
  return res;
}

async function errorFrom(res: Response): Promise<ApiError> {
  const data = (await res.json().catch(() => ({}))) as { error?: string };
  return new ApiError(res.status, data.error || res.statusText || "request failed", res.headers);
}

/** Sends a request and returns the JSON response; throws ApiError on errors. */
export async function api<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await apiFetch(path, opts);
  if (!res.ok) throw await errorFrom(res);
  if (res.status === 204) return null as T;
  return (await res.json()) as T;
}

/** Logs out this session on the server and forgets it here. */
export async function logout() {
  const token = storedRefreshToken();
  setSession(null);
  if (token) {
    await fetch(API_URL + "/auth/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: token }),
    }).catch(() => undefined);
  }
}

export interface UploadResult<T> {
  status: number;
  data: T;
  replayed: boolean; // the Idempotency-Key was already used: existing result returned
}

/**
 * Uploads a multipart form with upload progress (fetch can't report it).
 * Refreshes the token and retries once on 401.
 */
export async function upload<T>(
  path: string,
  form: FormData,
  opts: { headers?: Record<string, string>; onProgress?: (loaded: number, total: number) => void; signal?: AbortSignal },
  retried = false,
): Promise<UploadResult<T>> {
  const token = await getAccessToken();
  const result = await new Promise<{ status: number; text: string; replayed: boolean }>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", API_URL + path);
    if (token) xhr.setRequestHeader("Authorization", "Bearer " + token);
    for (const [k, v] of Object.entries(opts.headers || {})) xhr.setRequestHeader(k, v);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) opts.onProgress?.(e.loaded, e.total);
    };
    xhr.onload = () =>
      resolve({
        status: xhr.status,
        text: xhr.responseText,
        replayed: xhr.getResponseHeader("Idempotent-Replayed") === "true",
      });
    xhr.onerror = () => reject(new ApiError(0, "network error: the upload did not reach the server"));
    xhr.onabort = () => reject(new DOMException("upload aborted", "AbortError"));
    opts.signal?.addEventListener("abort", () => xhr.abort());
    xhr.send(form);
  });
  if (result.status === 401 && token && !retried && (await refreshSession())) {
    return upload(path, form, opts, true);
  }
  let data: unknown = null;
  try {
    data = JSON.parse(result.text);
  } catch {
    // not JSON
  }
  if (result.status < 200 || result.status >= 300) {
    const msg = (data as { error?: string } | null)?.error || `upload failed (HTTP ${result.status})`;
    throw new ApiError(result.status, msg);
  }
  return { status: result.status, data: data as T, replayed: result.replayed };
}

/** A random key for Idempotency-Key headers. */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}
