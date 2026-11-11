-- Every route must have an owner. Routes uploaded before accounts existed
-- (owner_id IS NULL) are deleted together with their versions.
DELETE FROM routes WHERE owner_id IS NULL;
ALTER TABLE routes ALTER COLUMN owner_id SET NOT NULL;

-- Same rules as before, without the special case for ownerless routes.
CREATE OR REPLACE FUNCTION route_role(p_route uuid, p_user uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT CASE
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
