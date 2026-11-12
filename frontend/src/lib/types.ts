export type Role = "owner" | "editor" | "viewer";
export type Visibility = "private" | "public";

export interface User {
  id: string;
  email: string;
  display_name: string;
  created_at: string;
}

export interface Session {
  access_token: string;
  token_type: string;
  expires_in: number;
  refresh_token: string;
  refresh_expires_in: number;
  user: User;
}

export interface Page<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}

export interface Route {
  id: string;
  name: string;
  description: string;
  original_filename: string;
  distance_m: number;
  ascent_m: number;
  descent_m: number;
  min_elevation_m: number | null;
  max_elevation_m: number | null;
  has_elevation: boolean;
  point_count: number;
  started_at: string | null;
  duration_s: number | null; // recorded track duration, when the file has timestamps
  visibility: Visibility;
  version: number;
  owner_id: string;
  owner_name: string;
  my_role: Role;
  created_at: string;
  updated_at: string;
}

export interface RouteVersion {
  version: number;
  change: "create" | "metadata" | "geometry" | "restore";
  restored_from: number | null;
  created_by_id: string | null;
  created_by_name: string | null;
  created_at: string;
  name: string;
  description: string;
  distance_m: number;
  ascent_m: number;
  descent_m: number;
  point_count: number;
}

export interface ProfilePoint {
  distance_m: number;
  elevation_m: number;
}

export interface Collection {
  id: string;
  name: string;
  description: string;
  visibility: Visibility;
  owner_id: string;
  owner_name: string;
  my_role: Role;
  route_count: number;
  created_at: string;
  updated_at: string;
}

export interface Share {
  user_id: string;
  email: string;
  display_name: string;
  role: "viewer" | "editor";
  created_at: string;
}

export interface AuditEvent {
  id: number;
  occurred_at: string;
  actor_id: string | null;
  actor_name: string | null;
  owner_id: string | null;
  action: string;
  subject_type: string;
  subject_id: string | null;
  details: Record<string, unknown>;
}

export type JobStatus = "queued" | "running" | "succeeded" | "failed" | "canceled";

export interface ImportJob {
  id: string;
  kind: "create" | "replace_geometry";
  status: JobStatus;
  stage: string;
  progress: number;
  revision: number;
  route_id: string | null;
  filename: string;
  name: string;
  visibility: Visibility;
  size_bytes: number;
  attempts: number;
  max_attempts: number;
  cancel_requested: boolean;
  error: string | null;
  run_after: string;
  created_at: string;
  updated_at: string;
  started_at: string | null;
  finished_at: string | null;
}

// A trip: a route you went on, when and for how long (private to you).
export interface Trip {
  id: string;
  name: string;
  notes: string;
  date: string; // YYYY-MM-DD
  duration_s: number;
  distance_m: number; // the route's, when the trip was logged
  ascent_m: number;
  route_id: string | null; // null once the route is deleted or hidden from you
  route_name: string | null;
  created_at: string;
  updated_at: string;
}

export interface TripStats {
  trips: number;
  duration_s: number;
  distance_m: number;
  ascent_m: number;
  first_date: string | null;
  last_date: string | null;
}

// Events on the user's event stream (GET /events).
export type UserEvent =
  | { type: "job"; job: ImportJob }
  | { type: "connected" }; // (re)connected: reload state that may be stale
