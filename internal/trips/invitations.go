package trips

import (
	"context"
	"errors"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/db"
)

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
