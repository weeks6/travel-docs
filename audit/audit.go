// Package audit records and lists audit events. Events are written with the
// same transaction as the change they describe, so either both are stored or
// neither is.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/db"
)

// Event describes something that happened. Empty IDs are stored as NULL.
type Event struct {
	ActorID     string
	OwnerID     string
	Action      string
	SubjectType string
	SubjectID   string
	Details     map[string]any
}

// Record inserts an event using tx.
func Record(ctx context.Context, tx pgx.Tx, e Event) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (actor_id, owner_id, action, subject_type, subject_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		NullID(e.ActorID), NullID(e.OwnerID), e.Action, e.SubjectType, NullID(e.SubjectID), string(b))
	if err != nil {
		return fmt.Errorf("record audit event %s: %w", e.Action, err)
	}
	return nil
}

// NullID turns an empty ID into SQL NULL.
func NullID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// StoredEvent is an event as returned by the API.
type StoredEvent struct {
	ID          int64           `json:"id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorID     *string         `json:"actor_id"`
	ActorName   *string         `json:"actor_name"`
	OwnerID     *string         `json:"owner_id"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   *string         `json:"subject_id"`
	Details     json.RawMessage `json:"details"`
}

// Store lists audit events.
type Store struct {
	Pool *pgxpool.Pool
}

// List returns events the user performed or that concern resources they own,
// newest first. beforeID paginates (0 = start from the newest).
func (s *Store) List(ctx context.Context, userID string, beforeID int64, limit int, subjectID string) ([]StoredEvent, error) {
	q := db.SQL.
		Select("e.id", "e.occurred_at", "e.actor_id::text", "u.display_name", "e.owner_id::text",
			"e.action", "e.subject_type", "e.subject_id::text", "e.details").
		From("audit_events e").
		LeftJoin("users u ON u.id = e.actor_id").
		Where(sq.Or{sq.Eq{"e.owner_id": userID}, sq.Eq{"e.actor_id": userID}}).
		OrderBy("e.id DESC").
		Limit(uint64(limit))
	if beforeID > 0 {
		q = q.Where(sq.Lt{"e.id": beforeID})
	}
	if subjectID != "" {
		q = q.Where(sq.Eq{"e.subject_id": subjectID})
	}
	query, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []StoredEvent{}
	for rows.Next() {
		var e StoredEvent
		var details []byte
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.ActorID, &e.ActorName, &e.OwnerID,
			&e.Action, &e.SubjectType, &e.SubjectID, &details); err != nil {
			return nil, err
		}
		e.Details = details
		events = append(events, e)
	}
	return events, rows.Err()
}

// Handler serves GET /audit-events.
type Handler struct {
	Store *Store
	// CurrentUser returns the authenticated user ID or an Unauthorized error.
	// (Injected because the auth package itself records audit events.)
	CurrentUser func(ctx context.Context) (string, error)
}

func (h *Handler) Register(e *echo.Echo) {
	e.GET("/audit-events", h.list)
}

// list supports ?before_id=, ?limit= (1–100, default 50) and ?subject_id=.
// The response includes next_before_id when more events may exist.
func (h *Handler) list(c *echo.Context) error {
	userID, err := h.CurrentUser(c.Request().Context())
	if err != nil {
		return err
	}
	limit, before := 50, int64(0)
	var subject string
	if err := echo.QueryParamsBinder(c).
		Int("limit", &limit).
		Int64("before_id", &before).
		String("subject_id", &subject).
		BindError(); err != nil {
		return apperr.New(apperr.Invalid, "limit and before_id must be integers")
	}
	if limit < 1 || limit > 100 {
		return apperr.New(apperr.Invalid, "limit must be between 1 and 100")
	}
	if before < 0 {
		return apperr.New(apperr.Invalid, "before_id must be a positive integer")
	}
	if _, err := uuid.Parse(subject); subject != "" && err != nil {
		return apperr.New(apperr.Invalid, "subject_id must be a UUID")
	}
	events, err := h.Store.List(c.Request().Context(), userID, before, limit, subject)
	if err != nil {
		return err
	}
	resp := map[string]any{"items": events}
	if len(events) == limit {
		resp["next_before_id"] = events[len(events)-1].ID
	}
	return c.JSON(http.StatusOK, resp)
}
