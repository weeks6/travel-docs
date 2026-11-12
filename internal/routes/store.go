package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/db"
	"github.com/weeks6/travel-docs/internal/paging"
	"github.com/weeks6/travel-docs/internal/routestats"
	"github.com/weeks6/travel-docs/internal/sharing"
)

// Store persists routes in PostgreSQL/PostGIS. Every method takes the acting
// user's ID ("" for anonymous) and applies the permission rules defined by
// the route_role() SQL function. Changes run in a transaction together with
// their version snapshot and audit event.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// selectRoutes selects route rows plus the actor's role ("" = anonymous).
// Column order matches scanRoute.
func selectRoutes(actor string) sq.SelectBuilder {
	return db.SQL.
		Select("r.id::text", "r.name", "r.description", "r.original_filename", "r.distance_m",
			"r.ascent_m", "r.descent_m", "r.min_elevation_m", "r.max_elevation_m", "r.has_elevation",
			"r.point_count", "r.started_at", "r.visibility", "r.version", "r.owner_id::text",
			"u.display_name").
		Column("route_role(r.id, ?)", nullID(actor)).
		Columns("r.created_at", "r.updated_at").
		From("routes r").
		Join("users u ON u.id = r.owner_id")
}

func scanRoute(row pgx.Row) (Route, error) {
	var r Route
	var role *string
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.OriginalFilename, &r.DistanceM, &r.AscentM,
		&r.DescentM, &r.MinElevationM, &r.MaxElevationM, &r.HasElevation, &r.PointCount,
		&r.StartedAt, &r.Visibility, &r.Version, &r.OwnerID, &r.OwnerName,
		&role, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
		return r, ErrNotFound
	}
	if role != nil {
		r.MyRole = *role
	}
	return r, err
}

// nullID turns "" (anonymous) into SQL NULL.
func nullID(id string) any { return audit.NullID(id) }

func (s *Store) get(ctx context.Context, q db.Querier, actor, id string) (Route, error) {
	query, args, err := selectRoutes(actor).Where(sq.Eq{"r.id": id}).ToSql()
	if err != nil {
		return Route{}, err
	}
	return scanRoute(q.QueryRow(ctx, query, args...))
}

// Get returns a route the actor can view.
func (s *Store) Get(ctx context.Context, actor, id string) (Route, error) {
	return s.get(ctx, s.pool, actor, id)
}

// Create stores a new route owned by actor as version 1.
func (s *Store) Create(ctx context.Context, actor string, c Content, visibility string) (Route, error) {
	var route Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		route, err = s.CreateInTx(ctx, tx, actor, c, visibility, nil)
		return err
	})
	return route, err
}

// CreateInTx is Create within the caller's transaction. Extra details are
// added to the audit event.
func (s *Store) CreateInTx(ctx context.Context, tx pgx.Tx, actor string, c Content, visibility string, details map[string]any) (Route, error) {
	if err := validateVisibility(visibility); err != nil {
		return Route{}, err
	}
	profile, err := json.Marshal(c.Profile)
	if err != nil {
		return Route{}, err
	}
	id := uuid.NewV7().String()
	_, err = tx.Exec(ctx, `
		INSERT INTO routes (id, owner_id, visibility, name, description, original_filename,
			distance_m, ascent_m, descent_m, min_elevation_m, max_elevation_m, has_elevation,
			point_count, started_at, geom, elevation_profile)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			ST_SetSRID(ST_GeomFromGeoJSON($15), 4326), $16)`,
		id, actor, visibility, c.Name, c.Description, c.OriginalFilename, c.DistanceM, c.AscentM,
		c.DescentM, c.MinElevationM, c.MaxElevationM, c.HasElevation, c.PointCount, c.StartedAt,
		string(c.Geometry), string(profile))
	if err != nil {
		return Route{}, fmt.Errorf("insert route: %w", err)
	}
	if err := snapshot(ctx, tx, id, "create", actor, nil); err != nil {
		return Route{}, err
	}
	d := map[string]any{"name": c.Name, "visibility": visibility}
	for k, v := range details {
		d[k] = v
	}
	if err := audit.Record(ctx, tx, audit.Event{
		ActorID: actor, OwnerID: actor, Action: "route.create", SubjectType: "route", SubjectID: id, Details: d,
	}); err != nil {
		return Route{}, err
	}
	return s.get(ctx, tx, actor, id)
}

// snapshot copies the route's current content into route_versions.
func snapshot(ctx context.Context, tx pgx.Tx, id, change, actor string, restoredFrom *int) error {
	query, args, err := db.SQL.
		Insert("route_versions").
		Columns(append([]string{"route_id", "version", "change", "restored_from", "created_by"}, contentColumns...)...).
		Select(db.SQL.
			Select("id", "version").
			Column("?::text", change).
			Column("?::integer", restoredFrom).
			Column("?::uuid", nullID(actor)).
			Columns(contentColumns...).
			From("routes").
			Where(sq.Eq{"id": id})).
		ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("snapshot route version: %w", err)
	}
	return nil
}

// contentColumns are the versioned columns shared by routes and route_versions.
var contentColumns = []string{"name", "description", "original_filename", "distance_m", "ascent_m",
	"descent_m", "min_elevation_m", "max_elevation_m", "point_count", "started_at", "has_elevation",
	"geom", "elevation_profile"}

// lockedRoute is the state of a route locked for a change.
type lockedRoute struct {
	OwnerID    string
	Version    int
	Visibility string
	Name       string
	Role       string
}

// lockForChange locks the route row and checks that the actor may edit it
// and, if ifVersion is non-zero, that it is still at that version
// (optimistic concurrency via If-Match).
func lockForChange(ctx context.Context, tx pgx.Tx, actor, id string, ifVersion int, ownerOnly bool) (lockedRoute, error) {
	var l lockedRoute
	var role *string
	err := tx.QueryRow(ctx, `
		SELECT owner_id::text, version, visibility, name, route_role(id, $2)
		FROM routes WHERE id = $1 FOR UPDATE`, id, nullID(actor),
	).Scan(&l.OwnerID, &l.Version, &l.Visibility, &l.Name, &role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Role = *role
	switch {
	case ownerOnly && l.Role != RoleOwner:
		return l, errOwnerOnly
	case l.Role == RoleViewer:
		return l, errReadOnly
	case ifVersion != 0 && ifVersion != l.Version:
		return l, errVersionConflict(l.Version)
	}
	return l, nil
}

// CheckEditable returns the route if the actor may change it and, when
// ifVersion is non-zero, it is still at that version. It takes no lock, so
// changes check again: use it to reject a request before reading a large
// body or queueing work.
func (s *Store) CheckEditable(ctx context.Context, actor, id string, ifVersion int) (Route, error) {
	r, err := s.Get(ctx, actor, id)
	switch {
	case err != nil:
		return r, err
	case r.MyRole == RoleViewer:
		return r, errReadOnly
	case ifVersion != 0 && ifVersion != r.Version:
		return r, errVersionConflict(r.Version)
	}
	return r, nil
}

func errVersionConflict(current int) error {
	return apperr.Newf(apperr.PreconditionFailed,
		"route was changed by someone else (now at version %d); reload and try again", current)
}

// UpdateMetadata changes name/description (creating a new version) and/or
// visibility (owner only; not versioned since it's access control).
func (s *Store) UpdateMetadata(ctx context.Context, actor, id string, upd MetadataUpdate, ifVersion int) (Route, error) {
	if upd.Name != nil {
		name, err := normalizeName(*upd.Name)
		if err != nil {
			return Route{}, err
		}
		upd.Name = &name
	}
	if upd.Description != nil {
		if err := validateDescription(*upd.Description); err != nil {
			return Route{}, err
		}
	}
	if upd.Visibility != nil {
		if err := validateVisibility(*upd.Visibility); err != nil {
			return Route{}, err
		}
	}

	var route Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockForChange(ctx, tx, actor, id, ifVersion, false)
		if err != nil {
			return err
		}
		current, err := s.get(ctx, tx, actor, id)
		if err != nil {
			return err
		}

		var fields []string
		if upd.Name != nil && *upd.Name != current.Name {
			fields = append(fields, "name")
		} else {
			upd.Name = &current.Name
		}
		if upd.Description != nil && *upd.Description != current.Description {
			fields = append(fields, "description")
		} else {
			upd.Description = &current.Description
		}
		if len(fields) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE routes SET name = $2, description = $3, version = version + 1, updated_at = now()
				WHERE id = $1`, id, *upd.Name, *upd.Description); err != nil {
				return err
			}
			if err := snapshot(ctx, tx, id, "metadata", actor, nil); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{
				ActorID: actor, OwnerID: l.OwnerID, Action: "route.update", SubjectType: "route", SubjectID: id,
				Details: map[string]any{"fields": fields, "version": l.Version + 1},
			}); err != nil {
				return err
			}
		}

		if upd.Visibility != nil && *upd.Visibility != l.Visibility {
			if l.Role != RoleOwner {
				return apperr.New(apperr.Forbidden, "only the route owner can change visibility")
			}
			if _, err := tx.Exec(ctx, `UPDATE routes SET visibility = $2, updated_at = now() WHERE id = $1`,
				id, *upd.Visibility); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{
				ActorID: actor, OwnerID: l.OwnerID, Action: "route.visibility_change", SubjectType: "route", SubjectID: id,
				Details: map[string]any{"from": l.Visibility, "to": *upd.Visibility},
			}); err != nil {
				return err
			}
		}

		route, err = s.get(ctx, tx, actor, id)
		return err
	})
	return route, err
}

// ReplaceGeometry stores new geometry and stats as a new version. When
// keepFileInfo is true (GeoJSON edits) the original file name and start time
// are kept, since the new geometry has neither.
func (s *Store) ReplaceGeometry(ctx context.Context, actor, id string, c Content, keepFileInfo bool, ifVersion int) (Route, error) {
	var route Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		route, err = s.ReplaceGeometryInTx(ctx, tx, actor, id, c, keepFileInfo, ifVersion, nil)
		return err
	})
	return route, err
}

// ReplaceGeometryInTx is ReplaceGeometry within the caller's transaction.
// Extra details are added to the audit event.
func (s *Store) ReplaceGeometryInTx(ctx context.Context, tx pgx.Tx, actor, id string, c Content, keepFileInfo bool, ifVersion int, details map[string]any) (Route, error) {
	profile, err := json.Marshal(c.Profile)
	if err != nil {
		return Route{}, err
	}
	l, err := lockForChange(ctx, tx, actor, id, ifVersion, false)
	if err != nil {
		return Route{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE routes SET
			original_filename = CASE WHEN $2 THEN original_filename ELSE $3 END,
			started_at        = CASE WHEN $2 THEN started_at ELSE $4 END,
			distance_m = $5, ascent_m = $6, descent_m = $7, min_elevation_m = $8,
			max_elevation_m = $9, has_elevation = $10, point_count = $11,
			geom = ST_SetSRID(ST_GeomFromGeoJSON($12), 4326), elevation_profile = $13,
			version = version + 1, updated_at = now()
		WHERE id = $1`,
		id, keepFileInfo, c.OriginalFilename, c.StartedAt, c.DistanceM, c.AscentM, c.DescentM,
		c.MinElevationM, c.MaxElevationM, c.HasElevation, c.PointCount, string(c.Geometry), string(profile))
	if err != nil {
		return Route{}, fmt.Errorf("update geometry: %w", err)
	}
	if err := snapshot(ctx, tx, id, "geometry", actor, nil); err != nil {
		return Route{}, err
	}
	source := "gpx"
	if keepFileInfo {
		source = "geojson"
	}
	d := map[string]any{"version": l.Version + 1, "source": source, "point_count": c.PointCount}
	for k, v := range details {
		d[k] = v
	}
	if err := audit.Record(ctx, tx, audit.Event{
		ActorID: actor, OwnerID: l.OwnerID, Action: "route.geometry_update", SubjectType: "route", SubjectID: id, Details: d,
	}); err != nil {
		return Route{}, err
	}
	return s.get(ctx, tx, actor, id)
}

// Restore makes an old version current again by saving it as a new version.
func (s *Store) Restore(ctx context.Context, actor, id string, version, ifVersion int) (Route, error) {
	var route Route
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockForChange(ctx, tx, actor, id, ifVersion, false)
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM route_versions WHERE route_id = $1 AND version = $2)`,
			id, version).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return apperr.New(apperr.NotFound, "version not found")
		}
		cols := strings.Join(contentColumns, ", ")
		if _, err := tx.Exec(ctx, `
			UPDATE routes r SET (`+cols+`) = (
				SELECT `+cols+` FROM route_versions v WHERE v.route_id = r.id AND v.version = $2
			), version = r.version + 1, updated_at = now()
			WHERE r.id = $1`, id, version); err != nil {
			return fmt.Errorf("restore version: %w", err)
		}
		if err := snapshot(ctx, tx, id, "restore", actor, &version); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "route.restore", SubjectType: "route", SubjectID: id,
			Details: map[string]any{"version": l.Version + 1, "restored_from": version},
		}); err != nil {
			return err
		}
		route, err = s.get(ctx, tx, actor, id)
		return err
	})
	return route, err
}

// Delete removes a route with its history. Only the owner may delete.
func (s *Store) Delete(ctx context.Context, actor, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockForChange(ctx, tx, actor, id, 0, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "route.delete", SubjectType: "route", SubjectID: id,
			Details: map[string]any{"name": l.Name},
		})
	})
}

var sortColumns = map[SortField]string{
	SortCreatedAt: "r.created_at",
	SortUpdatedAt: "r.updated_at",
	SortName:      "lower(r.name)",
	SortDistance:  "r.distance_m",
	SortAscent:    "r.ascent_m",
}

// List returns one page of routes the actor can see.
func (s *Store) List(ctx context.Context, actor string, p ListParams) (ListResult, error) {
	page := paging.Params{Page: p.Page, PageSize: p.PageSize}
	res := paging.Empty[Route](page)
	if (p.Scope == ScopeMine || p.Scope == ScopeShared) && actor == "" {
		return res, apperr.New(apperr.Unauthorized, "log in to list your own or shared routes")
	}
	actorID := nullID(actor)

	filter := sq.And{sq.Expr("route_role(r.id, ?) IS NOT NULL", actorID)}
	switch p.Scope {
	case ScopeMine:
		filter = append(filter, sq.Eq{"r.owner_id": actorID})
	case ScopeShared:
		filter = append(filter, sq.Expr(
			"EXISTS (SELECT 1 FROM route_shares rs WHERE rs.route_id = r.id AND rs.user_id = ?)", actorID))
	case ScopePublic:
		filter = append(filter, sq.Eq{"r.visibility": VisibilityPublic})
	}

	if p.CollectionID != "" {
		var role *string
		err := s.pool.QueryRow(ctx, `SELECT collection_role($1, $2)`, p.CollectionID, actorID).Scan(&role)
		if err != nil {
			return res, err
		}
		if role == nil {
			return res, apperr.New(apperr.NotFound, "collection not found")
		}
		filter = append(filter, sq.Expr(
			"EXISTS (SELECT 1 FROM collection_routes cr WHERE cr.route_id = r.id AND cr.collection_id = ?)", p.CollectionID))
	}
	if p.Name != "" {
		filter = append(filter, sq.ILike{"r.name": "%" + escapeLike(p.Name) + "%"})
	}
	if p.MinDistanceM != nil {
		filter = append(filter, sq.GtOrEq{"r.distance_m": *p.MinDistanceM})
	}
	if p.MaxDistanceM != nil {
		filter = append(filter, sq.LtOrEq{"r.distance_m": *p.MaxDistanceM})
	}
	if p.MinAscentM != nil {
		filter = append(filter, sq.GtOrEq{"r.ascent_m": *p.MinAscentM})
	}
	if p.MaxAscentM != nil {
		filter = append(filter, sq.LtOrEq{"r.ascent_m": *p.MaxAscentM})
	}
	if b := p.BBox; b != nil {
		filter = append(filter, sq.Expr("ST_Intersects(r.geom, ST_MakeEnvelope(?, ?, ?, ?, 4326))",
			b.MinLon, b.MinLat, b.MaxLon, b.MaxLat))
	}

	col, ok := sortColumns[p.Sort]
	if !ok {
		col = sortColumns[SortCreatedAt]
	}
	dir := " ASC"
	if p.Desc {
		dir = " DESC"
	}
	return db.ListPage(ctx, s.pool,
		db.SQL.Select("count(*)").From("routes r").Join("users u ON u.id = r.owner_id").Where(filter),
		selectRoutes(actor).Where(filter).OrderBy(col+dir, "r.id"+dir), // id is a tie-breaker so pages are stable
		page, scanRoute)
}

const geoJSONExpr = `CASE WHEN has_elevation THEN ST_AsGeoJSON(geom, 7) ELSE ST_AsGeoJSON(ST_Force2D(geom), 7) END`

// Geometry returns the current geometry as a GeoJSON geometry object;
// coordinates include elevation when the route has it.
func (s *Store) Geometry(ctx context.Context, actor, id string) (json.RawMessage, error) {
	query, args, err := db.SQL.Select(geoJSONExpr).From("routes").
		Where(sq.Eq{"id": id}).
		Where("route_role(id, ?) IS NOT NULL", nullID(actor)).
		ToSql()
	if err != nil {
		return nil, err
	}
	var geom string
	err = s.pool.QueryRow(ctx, query, args...).Scan(&geom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(geom), err
}

func (s *Store) ElevationProfile(ctx context.Context, actor, id string) ([]routestats.ProfilePoint, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT elevation_profile FROM routes WHERE id = $1 AND route_role(id, $2) IS NOT NULL`,
		id, nullID(actor)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	profile := []routestats.ProfilePoint{}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("decode elevation profile: %w", err)
	}
	return profile, nil
}

// Versions lists the history of a route, newest first.
func (s *Store) Versions(ctx context.Context, actor, id string) ([]Version, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT v.version, v.change, v.restored_from, v.created_by::text, u.display_name, v.created_at,
		       v.name, v.description, v.distance_m, v.ascent_m, v.descent_m, v.point_count
		FROM route_versions v LEFT JOIN users u ON u.id = v.created_by
		WHERE v.route_id = $1 ORDER BY v.version DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Version, error) {
		var v Version
		err := row.Scan(&v.Version, &v.Change, &v.RestoredFrom, &v.CreatedByID, &v.CreatedByName, &v.CreatedAt,
			&v.Name, &v.Description, &v.DistanceM, &v.AscentM, &v.DescentM, &v.PointCount)
		return v, err
	})
}

// VersionGeometry returns the geometry of an old version.
func (s *Store) VersionGeometry(ctx context.Context, actor, id string, version int) (json.RawMessage, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	query, args, err := db.SQL.Select(geoJSONExpr).From("route_versions").
		Where(sq.Eq{"route_id": id, "version": version}).
		ToSql()
	if err != nil {
		return nil, err
	}
	var geom string
	err = s.pool.QueryRow(ctx, query, args...).Scan(&geom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.New(apperr.NotFound, "version not found")
	}
	return json.RawMessage(geom), err
}

// Shares lists who the route is shared with. Owner only.
func (s *Store) Shares(ctx context.Context, actor, id string) ([]sharing.Share, error) {
	r, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if r.MyRole != RoleOwner {
		return nil, errOwnerOnly
	}
	return sharing.List(ctx, s.pool, sharing.Routes, id)
}

// PutShare shares the route with a user by email. Owner only.
func (s *Store) PutShare(ctx context.Context, actor, id, email, role string) (sharing.Share, error) {
	var share sharing.Share
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockForChange(ctx, tx, actor, id, 0, true)
		if err != nil {
			return err
		}
		share, err = sharing.Put(ctx, tx, sharing.Routes, id, l.OwnerID, actor, email, role)
		return err
	})
	return share, err
}

// DeleteShare removes a user's access. The owner can remove anyone; a user
// can remove their own share (leave).
func (s *Store) DeleteShare(ctx context.Context, actor, id, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockForChange(ctx, tx, actor, id, 0, actor != userID)
		if err != nil && !(actor == userID && errors.Is(err, errReadOnly)) {
			return err
		}
		return sharing.Delete(ctx, tx, sharing.Routes, id, l.OwnerID, actor, userID)
	})
}

// escapeLike escapes LIKE wildcards so user input is matched literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
