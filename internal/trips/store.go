package trips

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/db"
	"github.com/weeks6/travel-docs/internal/paging"
)

// Store persists trips. Methods take the acting user's ID.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, now: time.Now} }

// selectTrips selects trips the actor participates in. Column order matches scanTrip.
func selectTrips(actor string) sq.SelectBuilder {
	return db.SQL.
		Select("t.id::text", "t.name", "t.description", "t.status").
		Column("CASE WHEN route_role(t.route_id, ?) IS NOT NULL THEN t.route_id::text END", actor).
		Column("CASE WHEN route_role(t.route_id, ?) IS NOT NULL THEN r.name END", actor).
		Columns("t.location_retention_days", "t.owner_id::text", "u.display_name", "p.role",
			"(SELECT count(*) FROM trip_participants x WHERE x.trip_id = t.id)",
			"t.started_at", "t.ended_at", "t.created_at", "t.updated_at").
		From("trips t").
		Join("users u ON u.id = t.owner_id").
		Join("trip_participants p ON p.trip_id = t.id AND p.user_id = ?", actor).
		LeftJoin("routes r ON r.id = t.route_id")
}

func scanTrip(row pgx.Row) (Trip, error) {
	var t Trip
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Status, &t.RouteID, &t.RouteName,
		&t.LocationRetentionDays, &t.OwnerID, &t.OwnerName, &t.MyRole, &t.ParticipantCount,
		&t.StartedAt, &t.EndedAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) get(ctx context.Context, q db.Querier, actor, id string) (Trip, error) {
	query, args, err := selectTrips(actor).Where(sq.Eq{"t.id": id}).ToSql()
	if err != nil {
		return Trip{}, err
	}
	return scanTrip(q.QueryRow(ctx, query, args...))
}

// Get returns a trip the actor participates in.
func (s *Store) Get(ctx context.Context, actor, id string) (Trip, error) {
	return s.get(ctx, s.pool, actor, id)
}

func (in *Input) validate(creating bool) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > 200 {
			return apperr.New(apperr.Invalid, "name must be 1–200 characters")
		}
		in.Name = &n
	} else if creating {
		return apperr.New(apperr.Invalid, "name is required")
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 5000 {
		return apperr.New(apperr.Invalid, "description must be at most 5000 characters")
	}
	if d := in.LocationRetentionDays; d != nil && (*d < 1 || *d > MaxRetentionDays) {
		return apperr.Newf(apperr.Invalid, "location_retention_days must be between 1 and %d", MaxRetentionDays)
	}
	if in.RouteID != nil && *in.RouteID != "" {
		id, err := uuid.Parse(*in.RouteID)
		if err != nil {
			return apperr.New(apperr.NotFound, "route not found")
		}
		r := id.String()
		in.RouteID = &r
	}
	return nil
}

// checkRoute verifies that the actor may see the route they attach.
func checkRoute(ctx context.Context, tx pgx.Tx, actor, routeID string) error {
	var role *string
	err := tx.QueryRow(ctx, `SELECT route_role($1, $2)`, routeID, actor).Scan(&role)
	if err != nil {
		return err
	}
	if role == nil {
		return apperr.New(apperr.NotFound, "route not found")
	}
	return nil
}

// Create starts a planned trip with the actor as owner and only participant.
func (s *Store) Create(ctx context.Context, actor string, in Input) (Trip, error) {
	if err := in.validate(true); err != nil {
		return Trip{}, err
	}
	desc, retention := "", DefaultRetentionDays
	if in.Description != nil {
		desc = *in.Description
	}
	if in.LocationRetentionDays != nil {
		retention = *in.LocationRetentionDays
	}
	var routeID any
	if in.RouteID != nil && *in.RouteID != "" {
		routeID = *in.RouteID
	}
	id := uuid.NewV7().String()
	var t Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if routeID != nil {
			if err := checkRoute(ctx, tx, actor, routeID.(string)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trips (id, owner_id, name, description, route_id, location_retention_days)
			VALUES ($1, $2, $3, $4, $5, $6)`, id, actor, *in.Name, desc, routeID, retention); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO trip_participants (trip_id, user_id, role) VALUES ($1, $2, 'owner')`,
			id, actor); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: actor, Action: "trip.create", SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"name": *in.Name},
		}); err != nil {
			return err
		}
		var err error
		t, err = s.get(ctx, tx, actor, id)
		return err
	})
	return t, err
}

// List returns trips the actor participates in, newest first. status
// filters by planned, active or ended ("" = all).
func (s *Store) List(ctx context.Context, actor, status string, page paging.Params) (ListResult, error) {
	where := sq.And{}
	if status != "" {
		where = append(where, sq.Eq{"t.status": status})
	}
	// Active trips first, then planned, then ended; newest first within each.
	return db.ListPage(ctx, s.pool,
		db.SQL.Select("count(*)").From("trips t").
			Join("trip_participants p ON p.trip_id = t.id AND p.user_id = ?", actor).Where(where),
		selectTrips(actor).Where(where).
			OrderBy("CASE t.status WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END", "t.created_at DESC", "t.id DESC"),
		page, scanTrip)
}

type lockedTrip struct {
	OwnerID string
	Status  string
	Name    string
	Role    string
}

// lockTrip locks the trip row and checks the actor participates (and owns
// it, with ownerOnly).
func lockTrip(ctx context.Context, tx pgx.Tx, actor, id string, ownerOnly bool) (lockedTrip, error) {
	var l lockedTrip
	var role *string
	err := tx.QueryRow(ctx, `
		SELECT t.owner_id::text, t.status, t.name, p.role
		FROM trips t LEFT JOIN trip_participants p ON p.trip_id = t.id AND p.user_id = $2
		WHERE t.id = $1 FOR UPDATE OF t`, id, actor).Scan(&l.OwnerID, &l.Status, &l.Name, &role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Role = *role
	if ownerOnly && l.Role != RoleOwner {
		return l, errOwnerOnly
	}
	return l, nil
}

// Update changes the trip's details. Owner only.
func (s *Store) Update(ctx context.Context, actor, id string, in Input) (Trip, error) {
	if err := in.validate(false); err != nil {
		return Trip{}, err
	}
	var t Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		var fields []string
		setRoute, routeID := false, any(nil)
		if in.RouteID != nil {
			setRoute = true
			if *in.RouteID != "" {
				if err := checkRoute(ctx, tx, actor, *in.RouteID); err != nil {
					return err
				}
				routeID = *in.RouteID
			}
			fields = append(fields, "route_id")
		}
		if in.Name != nil {
			fields = append(fields, "name")
		}
		if in.Description != nil {
			fields = append(fields, "description")
		}
		if in.LocationRetentionDays != nil {
			fields = append(fields, "location_retention_days")
		}
		if len(fields) == 0 {
			t, err = s.get(ctx, tx, actor, id)
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE trips SET name = COALESCE($2, name), description = COALESCE($3, description),
				route_id = CASE WHEN $4::boolean THEN $5::uuid ELSE route_id END,
				location_retention_days = COALESCE($6, location_retention_days), updated_at = now()
			WHERE id = $1`, id, in.Name, in.Description, setRoute, routeID, in.LocationRetentionDays); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "trip.update", SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"fields": fields},
		}); err != nil {
			return err
		}
		t, err = s.get(ctx, tx, actor, id)
		return err
	})
	return t, err
}

// Delete removes the trip with its participants, invitations and
// location history. Owner only.
func (s *Store) Delete(ctx context.Context, actor, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM trips WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "trip.delete", SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"name": l.Name},
		})
	})
}

// SetStatus starts (planned → active) or ends (active → ended) the trip.
// Owner only.
func (s *Store) SetStatus(ctx context.Context, actor, id, to string) (Trip, error) {
	var t Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		var sql, action string
		switch {
		case to == StatusActive && l.Status == StatusPlanned:
			sql, action = `UPDATE trips SET status = 'active', started_at = now(), updated_at = now() WHERE id = $1`, "trip.start"
		case to == StatusEnded && l.Status == StatusActive:
			sql, action = `UPDATE trips SET status = 'ended', ended_at = now(), updated_at = now() WHERE id = $1`, "trip.end"
		case to == l.Status:
			return apperr.Newf(apperr.Conflict, "the trip is already %s", l.Status)
		case to == StatusActive:
			return apperr.New(apperr.Conflict, "an ended trip can't be started again")
		default:
			return apperr.New(apperr.Conflict, "only an active trip can be ended")
		}
		if _, err := tx.Exec(ctx, sql, id); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: action, SubjectType: "trip", SubjectID: id,
		}); err != nil {
			return err
		}
		t, err = s.get(ctx, tx, actor, id)
		return err
	})
	return t, err
}
