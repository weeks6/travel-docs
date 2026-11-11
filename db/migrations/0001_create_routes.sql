CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE routes (
    id                uuid PRIMARY KEY,
    name              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    original_filename text NOT NULL,
    distance_m        double precision NOT NULL,
    ascent_m          double precision NOT NULL,
    descent_m         double precision NOT NULL,
    min_elevation_m   double precision,
    max_elevation_m   double precision,
    point_count       integer NOT NULL,
    started_at        timestamptz,
    geom              geometry(MultiLineString, 4326) NOT NULL,
    elevation_profile jsonb NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX routes_geom_idx ON routes USING gist (geom);
CREATE INDEX routes_created_at_idx ON routes (created_at DESC, id);
CREATE INDEX routes_distance_idx ON routes (distance_m);
