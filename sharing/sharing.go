// Package sharing manages per-user shares of routes and collections.
// Callers check that the actor is allowed to manage shares (the owner)
// before calling Put or Delete.
package sharing

import (
	"context"
	"errors"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/audit"
	"github.com/weeks6/travel-docs/db"
)

const (
	RoleViewer = "viewer"
	RoleEditor = "editor"
)

// Spec describes a share table.
type Spec struct {
	Table       string // e.g. route_shares
	IDColumn    string // e.g. route_id
	SubjectType string // e.g. route (used for audit actions)
}

var (
	Routes      = Spec{Table: "route_shares", IDColumn: "route_id", SubjectType: "route"}
	Collections = Spec{Table: "collection_shares", IDColumn: "collection_id", SubjectType: "collection"}
)

// Share is a user's access to a resource.
type Share struct {
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// List returns all shares of a resource.
func List(ctx context.Context, q db.Querier, spec Spec, resourceID string) ([]Share, error) {
	query, args, err := db.SQL.
		Select("u.id::text", "u.email", "u.display_name", "s.role", "s.created_at").
		From(spec.Table + " s").
		Join("users u ON u.id = s.user_id").
		Where(sq.Eq{"s." + spec.IDColumn: resourceID}).
		OrderBy("s.created_at").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Share, error) {
		var s Share
		err := row.Scan(&s.UserID, &s.Email, &s.DisplayName, &s.Role, &s.CreatedAt)
		return s, err
	})
}

// Put shares a resource with the user registered under email, or changes
// their role if it is already shared with them.
func Put(ctx context.Context, tx pgx.Tx, spec Spec, resourceID, ownerID, actorID, email, role string) (Share, error) {
	if role != RoleViewer && role != RoleEditor {
		return Share{}, apperr.New(apperr.Invalid, "role must be viewer or editor")
	}
	var s Share
	err := tx.QueryRow(ctx, `SELECT id::text, email, display_name FROM users WHERE lower(email) = lower($1)`,
		strings.TrimSpace(email)).Scan(&s.UserID, &s.Email, &s.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, apperr.New(apperr.NotFound, "no user with this email")
	}
	if err != nil {
		return s, err
	}
	if s.UserID == ownerID {
		return s, apperr.Newf(apperr.Invalid, "the owner already has full access to this %s", spec.SubjectType)
	}

	query, args, err := db.SQL.
		Insert(spec.Table).
		Columns(spec.IDColumn, "user_id", "role").
		Values(resourceID, s.UserID, role).
		Suffix("ON CONFLICT (" + spec.IDColumn + ", user_id) DO UPDATE SET role = EXCLUDED.role RETURNING role, created_at").
		ToSql()
	if err != nil {
		return s, err
	}
	if err := tx.QueryRow(ctx, query, args...).Scan(&s.Role, &s.CreatedAt); err != nil {
		return s, err
	}
	return s, audit.Record(ctx, tx, audit.Event{
		ActorID: actorID, OwnerID: ownerID, Action: spec.SubjectType + ".share",
		SubjectType: spec.SubjectType, SubjectID: resourceID,
		Details: map[string]any{"user_id": s.UserID, "email": s.Email, "role": role},
	})
}

// Delete removes a user's share.
func Delete(ctx context.Context, tx pgx.Tx, spec Spec, resourceID, ownerID, actorID, userID string) error {
	query, args, err := db.SQL.
		Delete(spec.Table).
		Where(sq.Eq{spec.IDColumn: resourceID, "user_id": userID}).
		ToSql()
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(apperr.NotFound, "share not found")
	}
	return audit.Record(ctx, tx, audit.Event{
		ActorID: actorID, OwnerID: ownerID, Action: spec.SubjectType + ".unshare",
		SubjectType: spec.SubjectType, SubjectID: resourceID,
		Details: map[string]any{"user_id": userID},
	})
}
