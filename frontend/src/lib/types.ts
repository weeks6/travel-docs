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

export type TripStatus = "planned" | "active" | "ended";

export interface Trip {
  id: string;
  name: string;
  description: string;
  status: TripStatus;
  route_id: string | null;
  route_name: string | null;
  location_retention_days: number;
  owner_id: string;
  owner_name: string;
  my_role: "owner" | "member";
  participant_count: number;
  started_at: string | null;
  ended_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface Position {
  lat: number;
  lon: number;
  ele?: number;
  accuracy_m?: number;
  speed_mps?: number;
  heading_deg?: number;
  recorded_at: string;
}

export interface Participant {
  user_id: string;
  display_name: string;
  role: "owner" | "member";
  joined_at: string;
  online: boolean;
  last_seen_at: string | null;
  last_position: Position | null;
}

export interface Invitation {
  id: string;
  trip_id: string;
  trip_name: string;
  trip_status: TripStatus;
  invitee_id: string;
  invitee_name: string;
  invitee_email?: string;
  invited_by_id: string | null;
  invited_by_name: string | null;
  status: "pending" | "accepted" | "declined" | "revoked";
  created_at: string;
  responded_at: string | null;
}

// Events on the user's event stream (GET /events).
export type UserEvent =
  | { type: "job"; job: ImportJob }
  | { type: "invitation"; invitation: Invitation }
  | { type: "connected" }; // (re)connected: reload state that may be stale
