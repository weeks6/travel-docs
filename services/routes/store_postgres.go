package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/routestats"
)

// PostgresStore stores routes in PostgreSQL/PostGIS.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const routeColumns = `id::text, name, description, original_filename, distance_m, ascent_m,
	descent_m, min_elevation_m, max_elevation_m, point_count, started_at, created_at`

func scanRoute(row pgx.Row) (Route, error) {
	var r Route
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.OriginalFilename, &r.DistanceM,
		&r.AscentM, &r.DescentM, &r.MinElevationM, &r.MaxElevationM, &r.PointCount,
		&r.StartedAt, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *PostgresStore) Create(ctx context.Context, r NewRoute) (Route, error) {
	profile, err := json.Marshal(r.Profile)
	if err != nil {
		return Route{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO routes (id, name, description, original_filename, distance_m, ascent_m,
			descent_m, min_elevation_m, max_elevation_m, point_count, started_at, geom, elevation_profile)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			ST_SetSRID(ST_GeomFromGeoJSON($12), 4326), $13)
		RETURNING `+routeColumns,
		r.ID, r.Name, r.Description, r.OriginalFilename, r.DistanceM, r.AscentM, r.DescentM,
		r.MinElevationM, r.MaxElevationM, r.PointCount, r.StartedAt, string(r.Geometry), string(profile))
	return scanRoute(row)
}

func (s *PostgresStore) Get(ctx context.Context, id string) (Route, error) {
	return scanRoute(s.pool.QueryRow(ctx, `SELECT `+routeColumns+` FROM routes WHERE id = $1`, id))
}

var sortColumns = map[SortField]string{
	SortCreatedAt: "created_at",
	SortName:      "lower(name)",
	SortDistance:  "distance_m",
	SortAscent:    "ascent_m",
}

func (s *PostgresStore) List(ctx context.Context, p ListParams) (ListResult, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	if p.Name != "" {
		add(`name ILIKE '%%' || $%d || '%%'`, escapeLike(p.Name))
	}
	if p.MinDistanceM != nil {
		add("distance_m >= $%d", *p.MinDistanceM)
	}
	if p.MaxDistanceM != nil {
		add("distance_m <= $%d", *p.MaxDistanceM)
	}
	if p.MinAscentM != nil {
		add("ascent_m >= $%d", *p.MinAscentM)
	}
	if p.MaxAscentM != nil {
		add("ascent_m <= $%d", *p.MaxAscentM)
	}
	if b := p.BBox; b != nil {
		args = append(args, b.MinLon, b.MinLat, b.MaxLon, b.MaxLat)
		n := len(args)
		where = append(where, fmt.Sprintf(
			"ST_Intersects(geom, ST_MakeEnvelope($%d, $%d, $%d, $%d, 4326))", n-3, n-2, n-1, n))
	}

	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	res := ListResult{Page: p.Page, PageSize: p.PageSize, Items: []Route{}}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM routes`+whereSQL, args...).Scan(&res.Total); err != nil {
		return res, fmt.Errorf("count routes: %w", err)
	}
	res.TotalPages = (res.Total + p.PageSize - 1) / p.PageSize

	col, ok := sortColumns[p.Sort]
	if !ok {
		col = sortColumns[SortCreatedAt]
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	// id is a tie-breaker so pages are stable.
	query := fmt.Sprintf(`SELECT %s FROM routes%s ORDER BY %s %s, id %s LIMIT %d OFFSET %d`,
		routeColumns, whereSQL, col, dir, dir, p.PageSize, (p.Page-1)*p.PageSize)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return res, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return res, err
		}
		res.Items = append(res.Items, r)
	}
	return res, rows.Err()
}

func (s *PostgresStore) Geometry(ctx context.Context, id string) (json.RawMessage, error) {
	var geom string
	err := s.pool.QueryRow(ctx, `SELECT ST_AsGeoJSON(geom, 7) FROM routes WHERE id = $1`, id).Scan(&geom)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(geom), err
}

func (s *PostgresStore) ElevationProfile(ctx context.Context, id string) ([]routestats.ProfilePoint, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT elevation_profile FROM routes WHERE id = $1`, id).Scan(&raw)
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

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// escapeLike escapes LIKE wildcards so user input is matched literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
