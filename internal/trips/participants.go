package trips

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
)

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
