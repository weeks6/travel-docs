-- Stage 4: group trips with live location sharing.

CREATE TABLE trips (
    id                      uuid PRIMARY KEY,
    owner_id                uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    name                    text NOT NULL,
    description             text NOT NULL DEFAULT '',
    -- Optional planned route, shown to participants who may see it.
    route_id                uuid REFERENCES routes ON DELETE SET NULL,
    status                  text NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'active', 'ended')),
    -- Location points older than this are deleted.
    location_retention_days integer NOT NULL DEFAULT 30 CHECK (location_retention_days BETWEEN 1 AND 365),
    started_at              timestamptz,
    ended_at                timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX trips_owner_idx ON trips (owner_id, created_at DESC);

-- Participants, with presence and their last known position.
CREATE TABLE trip_participants (
    trip_id          uuid NOT NULL REFERENCES trips ON DELETE CASCADE,
    user_id          uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role             text NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at        timestamptz NOT NULL DEFAULT now(),
    -- Connected right now (as of last_seen_at; stale after a minute or so).
    online           boolean NOT NULL DEFAULT false,
    last_seen_at     timestamptz,
    last_lat         double precision,
    last_lon         double precision,
    last_ele         double precision,
    last_accuracy_m  double precision,
    last_speed_mps   double precision,
    last_heading_deg double precision,
    last_recorded_at timestamptz,
    PRIMARY KEY (trip_id, user_id)
);
CREATE INDEX trip_participants_user_idx ON trip_participants (user_id);

CREATE TABLE trip_invitations (
    id           uuid PRIMARY KEY,
    trip_id      uuid NOT NULL REFERENCES trips ON DELETE CASCADE,
    invitee_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    invited_by   uuid REFERENCES users ON DELETE SET NULL,
    status       text NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'accepted', 'declined', 'revoked')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    responded_at timestamptz
);
CREATE UNIQUE INDEX trip_invitations_pending_key ON trip_invitations (trip_id, invitee_id)
    WHERE status = 'pending';
CREATE INDEX trip_invitations_invitee_idx ON trip_invitations (invitee_id, created_at DESC);
CREATE INDEX trip_invitations_trip_idx ON trip_invitations (trip_id, created_at DESC);

-- Location history. received_at (server time) drives retention, since
-- recorded_at comes from the device.
CREATE TABLE trip_locations (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_id     uuid NOT NULL REFERENCES trips ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    lat         double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon         double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    ele         double precision,
    accuracy_m  double precision,
    speed_mps   double precision,
    heading_deg double precision
);
CREATE INDEX trip_locations_history_idx ON trip_locations (trip_id, user_id, recorded_at);
CREATE INDEX trip_locations_retention_idx ON trip_locations (trip_id, received_at);
