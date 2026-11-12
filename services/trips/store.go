package trips

import (
	"context"
	"errors"
	"fmt"
	"github.com/weeks6/travel-docs/internal/paging"
	"math"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/audit"
	"github.com/weeks6/travel-docs/db"
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

// Participants lists the trip's participants with presence and last
// known positions. Participants only.
func (s *Store) Participants(ctx context.Context, actor, id string) ([]Participant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.user_id::text, u.display_name, p.role, p.joined_at,
		       p.online AND p.last_seen_at > now() - make_interval(secs => $3), p.last_seen_at,
		       p.last_lat, p.last_lon, p.last_ele, p.last_accuracy_m, p.last_speed_mps,
		       p.last_heading_deg, p.last_recorded_at
		FROM trip_participants p JOIN users u ON u.id = p.user_id
		WHERE p.trip_id = $1
		  AND EXISTS (SELECT 1 FROM trip_participants me WHERE me.trip_id = $1 AND me.user_id = $2)
		ORDER BY p.role = 'owner' DESC, lower(u.display_name), p.user_id`,
		id, actor, PresenceTimeout.Seconds())
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Participant, error) {
		var p Participant
		var lat, lon *float64
		var pos Position
		var recorded *time.Time
		err := row.Scan(&p.UserID, &p.DisplayName, &p.Role, &p.JoinedAt, &p.Online, &p.LastSeenAt,
			&lat, &lon, &pos.Ele, &pos.AccuracyM, &pos.SpeedMps, &pos.HeadingDeg, &recorded)
		if lat != nil && lon != nil && recorded != nil {
			pos.Lat, pos.Lon, pos.RecordedAt = *lat, *lon, *recorded
			p.LastPosition = &pos
		}
		return p, err
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 { // the owner always participates: no access
		return nil, ErrNotFound
	}
	return list, nil
}

// Membership returns the user's role in the trip and the trip's status.
func (s *Store) Membership(ctx context.Context, tripID, userID string) (role, status string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT p.role, t.status FROM trips t JOIN trip_participants p ON p.trip_id = t.id AND p.user_id = $2
		WHERE t.id = $1`, tripID, userID).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return role, status, err
}

// RemoveParticipant removes userID from the trip together with their
// location history. The owner can remove members; members can remove
// themselves (leave).
func (s *Store) RemoveParticipant(ctx context.Context, actor, id, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, id, false)
		if err != nil {
			return err
		}
		if actor != userID && l.Role != RoleOwner {
			return errOwnerOnly
		}
		if userID == l.OwnerID {
			return apperr.New(apperr.Invalid, "the owner can't leave the trip; delete it instead")
		}
		tag, err := tx.Exec(ctx, `DELETE FROM trip_participants WHERE trip_id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(apperr.NotFound, "participant not found")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM trip_locations WHERE trip_id = $1 AND user_id = $2`, id, userID); err != nil {
			return err
		}
		action := "trip.participant_remove"
		if actor == userID {
			action = "trip.leave"
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: action, SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"user_id": userID, "name": l.Name},
		})
	})
}

const invitationSelect = `
	SELECT i.id::text, i.trip_id::text, t.name, t.status, i.invitee_id::text, iu.display_name, iu.email,
	       i.invited_by::text, bu.display_name, i.status, i.created_at, i.responded_at
	FROM trip_invitations i
	JOIN trips t ON t.id = i.trip_id
	JOIN users iu ON iu.id = i.invitee_id
	LEFT JOIN users bu ON bu.id = i.invited_by`

func scanInvitation(row pgx.Row) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.TripID, &i.TripName, &i.TripStatus, &i.InviteeID, &i.InviteeName, &i.InviteeEmail,
		&i.InvitedByID, &i.InvitedByName, &i.Status, &i.CreatedAt, &i.RespondedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, errInvitationGone
	}
	return i, err
}

func (s *Store) invitation(ctx context.Context, q db.Querier, id string) (Invitation, error) {
	return scanInvitation(q.QueryRow(ctx, invitationSelect+` WHERE i.id = $1`, id))
}

// Invite invites a registered user by email. Owner only.
func (s *Store) Invite(ctx context.Context, actor, id, email string) (Invitation, error) {
	var inv Invitation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if l.Status == StatusEnded {
			return apperr.New(apperr.Conflict, "the trip has ended")
		}
		var inviteeID, inviteeEmail string
		err = tx.QueryRow(ctx, `SELECT id::text, email FROM users WHERE lower(email) = lower($1)`,
			strings.TrimSpace(email)).Scan(&inviteeID, &inviteeEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.New(apperr.NotFound, "no user with this email")
		}
		if err != nil {
			return err
		}
		var participant bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trip_participants WHERE trip_id = $1 AND user_id = $2)`,
			id, inviteeID).Scan(&participant); err != nil {
			return err
		}
		if participant {
			return apperr.New(apperr.Conflict, "this user already participates in the trip")
		}
		invID := uuid.NewV7().String()
		_, err = tx.Exec(ctx, `INSERT INTO trip_invitations (id, trip_id, invitee_id, invited_by) VALUES ($1, $2, $3, $4)`,
			invID, id, inviteeID, actor)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return apperr.New(apperr.Conflict, "this user already has a pending invitation")
		}
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "trip.invite", SubjectType: "trip", SubjectID: id,
			Details: map[string]any{"user_id": inviteeID, "email": inviteeEmail, "name": l.Name},
		}); err != nil {
			return err
		}
		inv, err = s.invitation(ctx, tx, invID)
		return err
	})
	return inv, err
}

// TripInvitations lists all invitations of a trip, newest first. Owner only.
func (s *Store) TripInvitations(ctx context.Context, actor, id string) ([]Invitation, error) {
	t, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if t.MyRole != RoleOwner {
		return nil, errOwnerOnly
	}
	rows, err := s.pool.Query(ctx, invitationSelect+` WHERE i.trip_id = $1 ORDER BY i.created_at DESC LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invitation, error) { return scanInvitation(row) })
}

// RevokeInvitation withdraws a pending invitation. Owner only.
func (s *Store) RevokeInvitation(ctx context.Context, actor, tripID, invID string) (Invitation, error) {
	var inv Invitation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lockTrip(ctx, tx, actor, tripID, true)
		if err != nil {
			return err
		}
		var inviteeID string
		err = tx.QueryRow(ctx, `
			UPDATE trip_invitations SET status = 'revoked', responded_at = now()
			WHERE id = $1 AND trip_id = $2 AND status = 'pending'
			RETURNING invitee_id::text`, invID, tripID).Scan(&inviteeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.New(apperr.NotFound, "pending invitation not found")
		}
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "trip.invite_revoke", SubjectType: "trip", SubjectID: tripID,
			Details: map[string]any{"user_id": inviteeID, "name": l.Name},
		}); err != nil {
			return err
		}
		inv, err = s.invitation(ctx, tx, invID)
		return err
	})
	return inv, err
}

// MyInvitations lists invitations sent to the actor, newest first; only
// pending ones unless all is set.
func (s *Store) MyInvitations(ctx context.Context, actor string, all bool) ([]Invitation, error) {
	q := invitationSelect + ` WHERE i.invitee_id = $1`
	if !all {
		q += ` AND i.status = 'pending'`
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY i.created_at DESC LIMIT 200`, actor)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invitation, error) { return scanInvitation(row) })
}

// RespondInvitation accepts (joining the trip) or declines an invitation
// sent to the actor.
func (s *Store) RespondInvitation(ctx context.Context, actor, invID string, accept bool) (Invitation, error) {
	var inv Invitation
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var tripID, status, ownerID, name string
		err := tx.QueryRow(ctx, `
			SELECT i.trip_id::text, i.status, t.owner_id::text, t.name
			FROM trip_invitations i JOIN trips t ON t.id = i.trip_id
			WHERE i.id = $1 AND i.invitee_id = $2
			FOR UPDATE OF i`, invID, actor).Scan(&tripID, &status, &ownerID, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvitationGone
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return apperr.Newf(apperr.Conflict, "this invitation was already %s", status)
		}
		newStatus, action := "declined", "trip.invite_decline"
		if accept {
			newStatus, action = "accepted", "trip.invite_accept"
			if _, err := tx.Exec(ctx, `
				INSERT INTO trip_participants (trip_id, user_id, role) VALUES ($1, $2, 'member')
				ON CONFLICT DO NOTHING`, tripID, actor); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE trip_invitations SET status = $2, responded_at = now() WHERE id = $1`,
			invID, newStatus); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: ownerID, Action: action, SubjectType: "trip", SubjectID: tripID,
			Details: map[string]any{"name": name},
		}); err != nil {
			return err
		}
		inv, err = s.invitation(ctx, tx, invID)
		return err
	})
	return inv, err
}

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

// SetPresence records that a participant connected (online) or the last
// of their connections closed.
func (s *Store) SetPresence(ctx context.Context, tripID, userID string, online bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE trip_participants SET online = $3, last_seen_at = now()
		WHERE trip_id = $1 AND user_id = $2`, tripID, userID, online)
	return err
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

// ExpirePresence marks participants offline who haven't been seen for a
// while (e.g. their server instance crashed).
func (s *Store) ExpirePresence(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE trip_participants SET online = false
		WHERE online AND (last_seen_at IS NULL OR last_seen_at < now() - make_interval(secs => $1))`,
		(2 * PresenceTimeout).Seconds())
	return err
}
