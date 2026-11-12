-- Trips become a personal log of routes you went on (date and time spent),
-- replacing the live group trips of 0005 and all their data.

DROP TABLE IF EXISTS trip_locations, trip_invitations, trip_participants, trips CASCADE;

-- How long the recorded track took (last minus first timestamp), when the
-- GPX file has timestamps. Versioned like the other content columns.
ALTER TABLE routes ADD COLUMN duration_s integer CHECK (duration_s >= 0);
ALTER TABLE route_versions ADD COLUMN duration_s integer;

CREATE TABLE trips (
    id         uuid PRIMARY KEY,
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    -- The route walked. Deleting it keeps the trip (with its stats below).
    route_id   uuid REFERENCES routes ON DELETE SET NULL,
    name       text NOT NULL,
    notes      text NOT NULL DEFAULT '',
    trip_date  date NOT NULL,
    duration_s integer NOT NULL CHECK (duration_s > 0),
    -- The route's stats when the trip was logged, so later edits of the
    -- route don't rewrite history.
    distance_m double precision NOT NULL DEFAULT 0,
    ascent_m   double precision NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX trips_owner_date_idx ON trips (owner_id, trip_date DESC, created_at DESC);
CREATE INDEX trips_route_idx ON trips (route_id);
