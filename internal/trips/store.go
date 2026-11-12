package trips

import (
	"context"
	"errors"
	"time"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/db"
	"github.com/weeks6/travel-docs/internal/paging"
	"github.com/weeks6/travel-docs/internal/routes"
)

// Store persists trips. Every method acts for the trip owner.
type Store struct {
	pool   *pgxpool.Pool
	routes *routes.Store
	now    func() time.Time
}

func NewStore(pool *pgxpool.Pool, routeStore *routes.Store) *Store {
	return &Store{pool: pool, routes: routeStore, now: time.Now}
}

// selectTrips selects the owner's trips; the route is shown only while the
// owner may still see it. Column order matches scanTrip.
func selectTrips(owner string) sq.SelectBuilder {
	return db.SQL.
		Select("t.id::text", "t.name", "t.notes", "t.trip_date::text", "t.duration_s", "t.distance_m",
			"t.ascent_m", "t.created_at", "t.updated_at").
		Column("CASE WHEN route_role(r.id, t.owner_id) IS NOT NULL THEN r.id::text END").
		Column("CASE WHEN route_role(r.id, t.owner_id) IS NOT NULL THEN r.name END").
		From("trips t").
		LeftJoin("routes r ON r.id = t.route_id").
		Where(sq.Eq{"t.owner_id": owner})
}

func scanTrip(row pgx.Row) (Trip, error) {
	var t Trip
	err := row.Scan(&t.ID, &t.Name, &t.Notes, &t.Date, &t.DurationS, &t.DistanceM, &t.AscentM,
		&t.CreatedAt, &t.UpdatedAt, &t.RouteID, &t.RouteName)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) get(ctx context.Context, q db.Querier, owner, id string) (Trip, error) {
	query, args, err := selectTrips(owner).Where(sq.Eq{"t.id": id}).ToSql()
	if err != nil {
		return Trip{}, err
	}
	return scanTrip(q.QueryRow(ctx, query, args...))
}

// Get returns one of the owner's trips.
func (s *Store) Get(ctx context.Context, owner, id string) (Trip, error) {
	return s.get(ctx, s.pool, owner, id)
}

// Create logs a trip on a route the owner can see.
func (s *Store) Create(ctx context.Context, owner string, in Input) (Trip, error) {
	if in.RouteID == nil || *in.RouteID == "" {
		return Trip{}, errRouteRequired
	}
	if _, err := uuid.Parse(*in.RouteID); err != nil {
		return Trip{}, errRouteGone
	}
	if err := in.validate(s.now()); err != nil {
		return Trip{}, err
	}
	var trip Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		route, err := s.routes.GetInTx(ctx, tx, owner, *in.RouteID)
		if errors.Is(err, routes.ErrNotFound) {
			return errRouteGone
		}
		if err != nil {
			return err
		}
		trip, err = s.createInTx(ctx, tx, owner, route, in, nil)
		return err
	})
	return trip, err
}

// CreateWithRoute stores an uploaded GPX file as a new route of the owner
// and logs a trip on it, in one transaction.
func (s *Store) CreateWithRoute(ctx context.Context, owner string, c routes.Content, visibility string, in Input) (Trip, error) {
	in.RouteID = nil
	if err := in.validate(s.now()); err != nil {
		return Trip{}, err
	}
	var trip Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		route, err := s.routes.CreateInTx(ctx, tx, owner, c, visibility, map[string]any{"for_trip": true})
		if err != nil {
			return err
		}
		trip, err = s.createInTx(ctx, tx, owner, route, in, map[string]any{"new_route": true})
		return err
	})
	return trip, err
}

func (s *Store) createInTx(ctx context.Context, tx pgx.Tx, owner string, r routes.Route, in Input, details map[string]any) (Trip, error) {
	name := r.Name
	if in.Name != nil && *in.Name != "" {
		name = *in.Name
	}
	notes := ""
	if in.Notes != nil {
		notes = *in.Notes
	}
	var date string
	switch {
	case in.Date != nil:
		date = *in.Date
	case r.StartedAt != nil:
		date = r.StartedAt.UTC().Format(dateLayout)
	default:
		return Trip{}, errNoDate
	}
	var duration int
	switch {
	case in.DurationS != nil:
		duration = *in.DurationS
	case r.DurationS != nil && *r.DurationS > 0:
		duration = *r.DurationS
	default:
		return Trip{}, errNoDuration
	}

	id := uuid.NewV7().String()
	if _, err := tx.Exec(ctx, `
		INSERT INTO trips (id, owner_id, route_id, name, notes, trip_date, duration_s, distance_m, ascent_m)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, owner, r.ID, name, notes, date, duration, r.DistanceM, r.AscentM); err != nil {
		return Trip{}, err
	}
	d := map[string]any{"name": name, "route_id": r.ID, "date": date, "duration_s": duration}
	for k, v := range details {
		d[k] = v
	}
	if err := audit.Record(ctx, tx, audit.Event{
		ActorID: owner, OwnerID: owner, Action: "trip.create", SubjectType: "trip", SubjectID: id, Details: d,
	}); err != nil {
		return Trip{}, err
	}
	return s.get(ctx, tx, owner, id)
}

// Update changes a trip's name, notes, date or duration.
func (s *Store) Update(ctx context.Context, owner, id string, in Input) (Trip, error) {
	if in.RouteID != nil {
		return Trip{}, errRouteChange
	}
	if err := in.validate(s.now()); err != nil {
		return Trip{}, err
	}
	if in.Name != nil && *in.Name == "" {
		return Trip{}, errNameEmpty
	}
	set := map[string]any{}
	changes := map[string]any{}
	add := func(col, key string, v any) {
		set[col] = v
		changes[key] = v
	}
	if in.Name != nil {
		add("name", "name", *in.Name)
	}
	if in.Notes != nil {
		set["notes"] = *in.Notes // not repeated in the audit log
	}
	if in.Date != nil {
		add("trip_date", "date", *in.Date)
	}
	if in.DurationS != nil {
		add("duration_s", "duration_s", *in.DurationS)
	}
	var trip Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if len(set) > 0 {
			query, args, err := db.SQL.Update("trips").SetMap(set).Set("updated_at", sq.Expr("now()")).
				Where(sq.Eq{"id": id, "owner_id": owner}).ToSql()
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, query, args...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
			if err := audit.Record(ctx, tx, audit.Event{
				ActorID: owner, OwnerID: owner, Action: "trip.update", SubjectType: "trip", SubjectID: id, Details: changes,
			}); err != nil {
				return err
			}
		}
		var err error
		trip, err = s.get(ctx, tx, owner, id)
		return err
	})
	return trip, err
}

// Delete removes a trip (not its route).
func (s *Store) Delete(ctx context.Context, owner, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `DELETE FROM trips WHERE id = $1 AND owner_id = $2 RETURNING name`, id, owner).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: owner, OwnerID: owner, Action: "trip.delete", SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"name": name},
		})
	})
}

// Filter narrows lists and stats.
type Filter struct {
	Year    int    // 0 = all years
	RouteID string // "" = all routes
}

func (f Filter) where(owner string) sq.And {
	w := sq.And{sq.Eq{"t.owner_id": owner}}
	if f.Year != 0 {
		w = append(w, sq.Expr("t.trip_date >= make_date(?, 1, 1) AND t.trip_date < make_date(?, 1, 1)", f.Year, f.Year+1))
	}
	if f.RouteID != "" {
		w = append(w, sq.Eq{"t.route_id": f.RouteID})
	}
	return w
}

// List returns one page of the owner's trips, most recent first.
func (s *Store) List(ctx context.Context, owner string, f Filter, page paging.Params) (ListResult, error) {
	where := f.where(owner)
	return db.ListPage(ctx, s.pool,
		db.SQL.Select("count(*)").From("trips t").Where(where),
		selectTrips(owner).Where(where).OrderBy("t.trip_date DESC", "t.created_at DESC", "t.id DESC"),
		page, scanTrip)
}

// Stats sums up the owner's trips.
func (s *Store) Stats(ctx context.Context, owner string, f Filter) (Stats, error) {
	query, args, err := db.SQL.
		Select("count(*)", "coalesce(sum(t.duration_s), 0)", "coalesce(sum(t.distance_m), 0)",
			"coalesce(sum(t.ascent_m), 0)", "min(t.trip_date)::text", "max(t.trip_date)::text").
		From("trips t").Where(f.where(owner)).ToSql()
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	err = s.pool.QueryRow(ctx, query, args...).
		Scan(&st.Trips, &st.DurationS, &st.DistanceM, &st.AscentM, &st.FirstDate, &st.LastDate)
	return st, err
}
