package trips

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/db"
)

// ValidatePosition checks a reported point. now is the server time.
func ValidatePosition(p *Position, now time.Time) error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(p.Lat) || !finite(p.Lon) || p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return apperr.New(apperr.Invalid, "lat must be in [-90, 90] and lon in [-180, 180]")
	}
	optional := []struct {
		v        *float64
		name     string
		min, max float64
	}{
		{p.Ele, "ele", -1000, 20000},
		{p.AccuracyM, "accuracy_m", 0, 1e6},
		{p.SpeedMps, "speed_mps", 0, 1000},
		{p.HeadingDeg, "heading_deg", 0, 360},
	}
	for _, o := range optional {
		if o.v != nil && (!finite(*o.v) || *o.v < o.min || *o.v > o.max) {
			return apperr.Newf(apperr.Invalid, "%s must be between %g and %g", o.name, o.min, o.max)
		}
	}
	if p.RecordedAt.IsZero() {
		p.RecordedAt = now
	}
	if p.RecordedAt.After(now.Add(MaxClockSkew)) {
		return apperr.New(apperr.Invalid, "recorded_at is in the future")
	}
	if p.RecordedAt.Before(now.Add(-MaxPointAge)) {
		return apperr.Newf(apperr.Invalid, "recorded_at is older than %s", MaxPointAge)
	}
	p.RecordedAt = p.RecordedAt.UTC().Truncate(time.Millisecond)
	return nil
}

// AddLocations stores points reported by a participant of an active trip
// and updates their last known position. It returns the newest point if it
// became the participant's last position (to broadcast it), else nil.
func (s *Store) AddLocations(ctx context.Context, tripID, userID string, pts []Position) (*Position, error) {
	if len(pts) == 0 {
		return nil, apperr.New(apperr.Invalid, "no points")
	}
	now := s.now()
	latest := 0
	n := len(pts)
	times := make([]time.Time, n)
	lats, lons := make([]float64, n), make([]float64, n)
	eles, accs, speeds, headings := make([]*float64, n), make([]*float64, n), make([]*float64, n), make([]*float64, n)
	for i := range pts {
		if err := ValidatePosition(&pts[i], now); err != nil {
			if n > 1 {
				return nil, apperr.Newf(apperr.Invalid, "point %d: %s", i+1, err)
			}
			return nil, err
		}
		p := pts[i]
		times[i], lats[i], lons[i] = p.RecordedAt, p.Lat, p.Lon
		eles[i], accs[i], speeds[i], headings[i] = p.Ele, p.AccuracyM, p.SpeedMps, p.HeadingDeg
		if p.RecordedAt.After(pts[latest].RecordedAt) {
			latest = i
		}
	}

	var newest *Position
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		// FOR SHARE: a concurrent removal waits, then deletes these points too.
		err := tx.QueryRow(ctx, `
			SELECT t.status FROM trips t JOIN trip_participants p ON p.trip_id = t.id AND p.user_id = $2
			WHERE t.id = $1 FOR SHARE OF p`, tripID, userID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != StatusActive {
			return errNotActive
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trip_locations (trip_id, user_id, recorded_at, lat, lon, ele, accuracy_m, speed_mps, heading_deg)
			SELECT $1, $2, * FROM unnest($3::timestamptz[], $4::float8[], $5::float8[], $6::float8[],
				$7::float8[], $8::float8[], $9::float8[])`,
			tripID, userID, times, lats, lons, eles, accs, speeds, headings); err != nil {
			return fmt.Errorf("insert locations: %w", err)
		}
		p := pts[latest]
		tag, err := tx.Exec(ctx, `
			UPDATE trip_participants SET last_lat = $3, last_lon = $4, last_ele = $5, last_accuracy_m = $6,
				last_speed_mps = $7, last_heading_deg = $8, last_recorded_at = $9
			WHERE trip_id = $1 AND user_id = $2 AND (last_recorded_at IS NULL OR last_recorded_at <= $9)`,
			tripID, userID, p.Lat, p.Lon, p.Ele, p.AccuracyM, p.SpeedMps, p.HeadingDeg, p.RecordedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			newest = &p
		}
		return nil
	})
	return newest, err
}

// HistoryParams selects location history.
type HistoryParams struct {
	UserID    string     // "" = everyone
	Since     *time.Time // optional
	Until     *time.Time // optional
	MaxPoints int        // per participant; evenly downsampled beyond this
}

// History returns each participant's track as GeoJSON features: a
// LineString (or a Point for a single fix) with [lon, lat] coordinates.
func (s *Store) History(ctx context.Context, actor, id string, p HistoryParams) (map[string]any, error) {
	if _, _, err := s.Membership(ctx, id, actor); err != nil {
		return nil, err
	}
	where := sq.And{sq.Eq{"l.trip_id": id}}
	if p.UserID != "" {
		where = append(where, sq.Eq{"l.user_id": p.UserID})
	}
	if p.Since != nil {
		where = append(where, sq.GtOrEq{"l.recorded_at": *p.Since})
	}
	if p.Until != nil {
		where = append(where, sq.LtOrEq{"l.recorded_at": *p.Until})
	}
	inner := db.SQL.Select("l.user_id", "l.recorded_at", "l.lat", "l.lon",
		"row_number() OVER (PARTITION BY l.user_id ORDER BY l.recorded_at, l.id) AS rn",
		"count(*) OVER (PARTITION BY l.user_id) AS n").
		From("trip_locations l").Where(where)
	query, args, err := db.SQL.
		Select("x.user_id::text", "u.display_name", "x.recorded_at", "x.lat", "x.lon", "x.n").
		FromSelect(inner, "x").
		Join("users u ON u.id = x.user_id").
		Where(sq.Expr("(x.n <= ? OR (x.rn - 1) % ceil(x.n::numeric / ?)::bigint = 0 OR x.rn = x.n)", p.MaxPoints, p.MaxPoints)).
		OrderBy("x.user_id", "x.rn").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type track struct {
		name       string
		coords     [][2]float64
		total      int64
		first, end time.Time
	}
	var order []string
	tracks := map[string]*track{}
	for rows.Next() {
		var userID, name string
		var at time.Time
		var lat, lon float64
		var n int64
		if err := rows.Scan(&userID, &name, &at, &lat, &lon, &n); err != nil {
			return nil, err
		}
		t := tracks[userID]
		if t == nil {
			t = &track{name: name, total: n, first: at}
			tracks[userID] = t
			order = append(order, userID)
		}
		t.coords = append(t.coords, [2]float64{lon, lat})
		t.end = at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	features := make([]map[string]any, 0, len(order))
	for _, userID := range order {
		t := tracks[userID]
		geom := map[string]any{"type": "LineString", "coordinates": t.coords}
		if len(t.coords) == 1 {
			geom = map[string]any{"type": "Point", "coordinates": t.coords[0]}
		}
		features = append(features, map[string]any{
			"type":     "Feature",
			"geometry": geom,
			"properties": map[string]any{
				"user_id": userID, "display_name": t.name, "point_count": t.total,
				"returned_points": len(t.coords), "first_recorded_at": t.first, "last_recorded_at": t.end,
			},
		})
	}
	return map[string]any{"type": "FeatureCollection", "features": features}, nil
}

// retentionBatch is how many points one cleanup statement deletes, so
// cleanup never holds locks for long.
const retentionBatch = 5000

// ApplyRetention deletes location points older than each trip's retention
// period, forgets last positions that old, and marks participants offline
// whose connection vanished without saying goodbye.
func (s *Store) ApplyRetention(ctx context.Context) (deleted int64, err error) {
	for {
		tag, err := s.pool.Exec(ctx, `
			DELETE FROM trip_locations WHERE id IN (
				SELECT l.id FROM trip_locations l JOIN trips t ON t.id = l.trip_id
				WHERE l.received_at < now() - make_interval(days => t.location_retention_days)
				LIMIT $1)`, retentionBatch)
		if err != nil {
			return deleted, fmt.Errorf("delete expired locations: %w", err)
		}
		deleted += tag.RowsAffected()
		if tag.RowsAffected() < retentionBatch {
			break
		}
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE trip_participants p SET last_lat = NULL, last_lon = NULL, last_ele = NULL, last_accuracy_m = NULL,
			last_speed_mps = NULL, last_heading_deg = NULL, last_recorded_at = NULL
		FROM trips t
		WHERE t.id = p.trip_id AND p.last_recorded_at < now() - make_interval(days => t.location_retention_days)`); err != nil {
		return deleted, fmt.Errorf("clear expired positions: %w", err)
	}
	return deleted, s.ExpirePresence(ctx)
}
