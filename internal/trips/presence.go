package trips

import (
	"context"
	"time"
)

// SetPresence records that a participant connected (online) or the last
// of their connections closed.
func (s *Store) SetPresence(ctx context.Context, tripID, userID string, online bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE trip_participants SET online = $3, last_seen_at = now()
		WHERE trip_id = $1 AND user_id = $2`, tripID, userID, online)
	return err
}

// ExpirePresence marks participants offline who haven't been seen for a
// while (e.g. their server instance crashed).
func (s *Store) ExpirePresence(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE trip_participants SET online = false
		WHERE online AND (last_seen_at IS NULL OR last_seen_at < now() - make_interval(secs => $1))`,
		(2 * PresenceTimeout).Seconds())
	return err
}

type presenceKey struct{ trip, user string }

// connected marks the participant online and tells the others.
func (h *Hub) connected(ctx context.Context, tripID, userID string) {
	h.mu.Lock()
	h.conns[presenceKey{tripID, userID}]++
	h.mu.Unlock()
	h.heartbeat(ctx, tripID, userID)
}

// heartbeat refreshes the participant's presence. Repeating the event
// heals presence shown as offline by another instance.
func (h *Hub) heartbeat(ctx context.Context, tripID, userID string) {
	if err := h.store.SetPresence(ctx, tripID, userID, true); err != nil && ctx.Err() == nil {
		h.log.Warn("update presence", "trip_id", tripID, "user_id", userID, "error", err)
	}
	h.publish(ctx, tripID, PresenceEvent{Type: EventPresence, UserID: userID, Online: true, At: time.Now().UTC()})
}

// disconnected marks the participant offline when their last connection
// on this instance closes.
func (h *Hub) disconnected(tripID, userID string) {
	k := presenceKey{tripID, userID}
	h.mu.Lock()
	h.conns[k]--
	last := h.conns[k] <= 0
	if last {
		delete(h.conns, k)
	}
	h.mu.Unlock()
	if !last {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.SetPresence(ctx, tripID, userID, false); err != nil {
		h.log.Warn("update presence", "trip_id", tripID, "user_id", userID, "error", err)
	}
	h.publish(ctx, tripID, PresenceEvent{Type: EventPresence, UserID: userID, Online: false, At: time.Now().UTC()})
}
