-- Stage 2: accounts, ownership and visibility, sharing, collections,
-- route version history and audit events.

CREATE TABLE users (
    id                 uuid PRIMARY KEY,
    email              text NOT NULL,
    password_hash      text NOT NULL,
    display_name       text NOT NULL,
    -- Access tokens issued before this moment are rejected (password change,
    -- "log out everywhere").
    tokens_valid_after timestamptz NOT NULL DEFAULT date_trunc('second', now()),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Refresh tokens are stored hashed. Each login starts a family; every refresh
-- revokes the used token and issues a new one in the same family. Presenting
-- an already revoked token revokes the whole family (token theft detection).
CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    family_id   uuid NOT NULL,
    token_hash  bytea NOT NULL UNIQUE,
    user_agent  text NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);

-- Routes uploaded before accounts existed have no owner. They are invisible
-- until the first user registers, who then takes ownership of them.
ALTER TABLE routes
    ADD COLUMN owner_id      uuid REFERENCES users ON DELETE CASCADE,
    ADD COLUMN visibility    text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
    ADD COLUMN version       integer NOT NULL DEFAULT 1,
    ADD COLUMN has_elevation boolean NOT NULL DEFAULT false,
    ADD COLUMN updated_at    timestamptz NOT NULL DEFAULT now();
UPDATE routes SET updated_at = created_at;

-- Keep elevation in the geometry so edited versions don't lose it. Existing
-- rows only had 2D geometry; their elevation profile is kept as is.
DROP INDEX routes_geom_idx;
ALTER TABLE routes ALTER COLUMN geom TYPE geometry(MultiLineStringZ, 4326) USING ST_Force3D(geom);
CREATE INDEX routes_geom_idx ON routes USING gist (geom);
CREATE INDEX routes_owner_idx ON routes (owner_id, created_at DESC);
CREATE INDEX routes_public_idx ON routes (created_at DESC) WHERE visibility = 'public';

-- Full snapshot of a route's content for every version.
CREATE TABLE route_versions (
    route_id          uuid NOT NULL REFERENCES routes ON DELETE CASCADE,
    version           integer NOT NULL,
    change            text NOT NULL CHECK (change IN ('create', 'metadata', 'geometry', 'restore')),
    restored_from     integer,
    created_by        uuid REFERENCES users ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    name              text NOT NULL,
    description       text NOT NULL,
    original_filename text NOT NULL,
    distance_m        double precision NOT NULL,
    ascent_m          double precision NOT NULL,
    descent_m         double precision NOT NULL,
    min_elevation_m   double precision,
    max_elevation_m   double precision,
    point_count       integer NOT NULL,
    started_at        timestamptz,
    has_elevation     boolean NOT NULL,
    geom              geometry(MultiLineStringZ, 4326) NOT NULL,
    elevation_profile jsonb NOT NULL,
    PRIMARY KEY (route_id, version)
);

INSERT INTO route_versions (route_id, version, change, created_at, name, description,
    original_filename, distance_m, ascent_m, descent_m, min_elevation_m, max_elevation_m,
    point_count, started_at, has_elevation, geom, elevation_profile)
SELECT id, 1, 'create', created_at, name, description, original_filename, distance_m,
    ascent_m, descent_m, min_elevation_m, max_elevation_m, point_count, started_at,
    has_elevation, geom, elevation_profile
FROM routes;

CREATE TABLE route_shares (
    route_id   uuid NOT NULL REFERENCES routes ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('viewer', 'editor')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (route_id, user_id)
);
CREATE INDEX route_shares_user_idx ON route_shares (user_id);

CREATE TABLE collections (
    id          uuid PRIMARY KEY,
    owner_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    visibility  text NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collections_owner_idx ON collections (owner_id, created_at DESC);

CREATE TABLE collection_routes (
    collection_id uuid NOT NULL REFERENCES collections ON DELETE CASCADE,
    route_id      uuid NOT NULL REFERENCES routes ON DELETE CASCADE,
    added_by      uuid REFERENCES users ON DELETE SET NULL,
    added_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, route_id)
);
CREATE INDEX collection_routes_route_idx ON collection_routes (route_id);

CREATE TABLE collection_shares (
    collection_id uuid NOT NULL REFERENCES collections ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role          text NOT NULL CHECK (role IN ('viewer', 'editor')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, user_id)
);
CREATE INDEX collection_shares_user_idx ON collection_shares (user_id);

-- Permission rules live in the database so every query applies them the same
-- way. Both return 'owner', 'editor', 'viewer' or NULL (no access).

CREATE FUNCTION collection_role(p_collection uuid, p_user uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN c.owner_id = p_user THEN 'owner'
        WHEN cs.role IS NOT NULL THEN cs.role
        WHEN c.visibility = 'public' THEN 'viewer'
    END
    FROM collections c
    LEFT JOIN collection_shares cs ON cs.collection_id = c.id AND cs.user_id = p_user
    WHERE c.id = p_collection
$$;

-- A route is visible through a collection only if the route's owner put it
-- there: adding your own route to a collection shares it with everyone who
-- can see the collection. Routes added by others stay subject to their own
-- visibility.
CREATE FUNCTION route_role(p_route uuid, p_user uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN r.owner_id IS NULL THEN NULL
        WHEN r.owner_id = p_user THEN 'owner'
        WHEN rs.role IS NOT NULL THEN rs.role
        WHEN r.visibility = 'public' THEN 'viewer'
        WHEN EXISTS (
            SELECT 1 FROM collection_routes cr
            WHERE cr.route_id = r.id
              AND cr.added_by = r.owner_id
              AND collection_role(cr.collection_id, p_user) IS NOT NULL
        ) THEN 'viewer'
    END
    FROM routes r
    LEFT JOIN route_shares rs ON rs.route_id = r.id AND rs.user_id = p_user
    WHERE r.id = p_route
$$;

CREATE TABLE audit_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    actor_id     uuid REFERENCES users ON DELETE SET NULL,
    -- Owner of the affected resource; events are visible to actor and owner.
    owner_id     uuid REFERENCES users ON DELETE CASCADE,
    action       text NOT NULL,
    subject_type text NOT NULL,
    subject_id   uuid,
    details      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_events_owner_idx ON audit_events (owner_id, id DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_id, id DESC);
CREATE INDEX audit_events_subject_idx ON audit_events (subject_id, id DESC);
