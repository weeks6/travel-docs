-- Where each route starts, for the map's route markers and clusters
-- (GET /routes/map). Stored and indexed so the map query never reads the
-- full track geometry; generated, so every geometry change keeps it current.
ALTER TABLE routes ADD COLUMN start_point geometry(Point, 4326)
    GENERATED ALWAYS AS (ST_Force2D(ST_StartPoint(ST_GeometryN(geom, 1)))) STORED;

CREATE INDEX routes_start_point_idx ON routes USING gist (start_point);
