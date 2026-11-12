package collections

import (
	"context"
	"errors"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/db"
	"github.com/weeks6/travel-docs/internal/paging"
	"github.com/weeks6/travel-docs/internal/sharing"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// selectCollections selects collection rows plus the actor's role and the
// number of routes visible to them. Column order matches scanCollection.
func selectCollections(actor string) sq.SelectBuilder {
	actorID := audit.NullID(actor)
	return db.SQL.
		Select("c.id::text", "c.name", "c.description", "c.visibility", "c.owner_id::text", "u.display_name").
		Column("collection_role(c.id, ?)", actorID).
		Column(`(SELECT count(*) FROM collection_routes cr
			WHERE cr.collection_id = c.id AND route_role(cr.route_id, ?) IS NOT NULL)`, actorID).
		Columns("c.created_at", "c.updated_at").
		From("collections c").
		Join("users u ON u.id = c.owner_id")
}

func scanCollection(row pgx.Row) (Collection, error) {
	var c Collection
	var role *string
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Visibility, &c.OwnerID, &c.OwnerName,
		&role, &c.RouteCount, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
		return c, ErrNotFound
	}
	if role != nil {
		c.MyRole = *role
	}
	return c, err
}

func (s *Store) get(ctx context.Context, q db.Querier, actor, id string) (Collection, error) {
	query, args, err := selectCollections(actor).Where(sq.Eq{"c.id": id}).ToSql()
	if err != nil {
		return Collection{}, err
	}
	return scanCollection(q.QueryRow(ctx, query, args...))
}

func (s *Store) Get(ctx context.Context, actor, id string) (Collection, error) {
	return s.get(ctx, s.pool, actor, id)
}

type locked struct {
	OwnerID string
	Name    string
	Role    string
}

func lock(ctx context.Context, tx pgx.Tx, actor, id string, ownerOnly bool) (locked, error) {
	var l locked
	var role *string
	err := tx.QueryRow(ctx, `
		SELECT owner_id::text, name, collection_role(id, $2) FROM collections WHERE id = $1 FOR UPDATE`,
		id, audit.NullID(actor)).Scan(&l.OwnerID, &l.Name, &role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.Role = *role
	if ownerOnly && l.Role != "owner" {
		return l, errOwnerOnly
	}
	if l.Role == "viewer" {
		return l, errReadOnly
	}
	return l, nil
}

func (s *Store) Create(ctx context.Context, actor string, in Input) (Collection, error) {
	if err := in.validate(true); err != nil {
		return Collection{}, err
	}
	desc, vis := "", "private"
	if in.Description != nil {
		desc = *in.Description
	}
	if in.Visibility != nil {
		vis = *in.Visibility
	}
	id := uuid.NewV7().String()
	var c Collection
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO collections (id, owner_id, name, description, visibility) VALUES ($1, $2, $3, $4, $5)`,
			id, actor, *in.Name, desc, vis); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: actor, Action: "collection.create", SubjectType: "collection", SubjectID: id,
			Details: map[string]any{"name": *in.Name, "visibility": vis},
		}); err != nil {
			return err
		}
		var err error
		c, err = s.get(ctx, tx, actor, id)
		return err
	})
	return c, err
}

func (s *Store) Update(ctx context.Context, actor, id string, in Input) (Collection, error) {
	if err := in.validate(false); err != nil {
		return Collection{}, err
	}
	var c Collection
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, false)
		if err != nil {
			return err
		}
		if in.Visibility != nil && l.Role != "owner" {
			return apperr.New(apperr.Forbidden, "only the collection owner can change visibility")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE collections SET
				name = COALESCE($2, name), description = COALESCE($3, description),
				visibility = COALESCE($4, visibility), updated_at = now()
			WHERE id = $1`, id, in.Name, in.Description, in.Visibility); err != nil {
			return err
		}
		var fields []string
		for name, set := range map[string]bool{"name": in.Name != nil, "description": in.Description != nil, "visibility": in.Visibility != nil} {
			if set {
				fields = append(fields, name)
			}
		}
		if err := audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "collection.update", SubjectType: "collection", SubjectID: id,
			Details: map[string]any{"fields": fields},
		}); err != nil {
			return err
		}
		c, err = s.get(ctx, tx, actor, id)
		return err
	})
	return c, err
}

// Delete removes the collection; its routes are not deleted.
func (s *Store) Delete(ctx context.Context, actor, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "collection.delete", SubjectType: "collection", SubjectID: id,
			Details: map[string]any{"name": l.Name},
		})
	})
}

// AddRoute adds a route the actor owns, or a public route.
func (s *Store) AddRoute(ctx context.Context, actor, id, routeID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, false)
		if err != nil {
			return err
		}
		var ownerID, visibility string
		var role *string
		err = tx.QueryRow(ctx, `SELECT owner_id::text, visibility, route_role(id, $2) FROM routes WHERE id = $1`,
			routeID, actor).Scan(&ownerID, &visibility, &role)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && role == nil) {
			return apperr.New(apperr.NotFound, "route not found")
		}
		if err != nil {
			return err
		}
		if ownerID != actor && visibility != "public" {
			return apperr.New(apperr.Forbidden, "you can only add your own routes or public routes")
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO collection_routes (collection_id, route_id, added_by) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, id, routeID, actor)
		if err != nil || tag.RowsAffected() == 0 {
			return err // already in the collection: nothing to do
		}
		if _, err := tx.Exec(ctx, `UPDATE collections SET updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "collection.route_add", SubjectType: "collection", SubjectID: id,
			Details: map[string]any{"route_id": routeID},
		})
	})
}

// RemoveRoute removes a route from the collection. Collection editors and the
// route's owner may do this.
func (s *Store) RemoveRoute(ctx context.Context, actor, id, routeID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, false)
		if errors.Is(err, errReadOnly) {
			var routeOwner string
			if e := tx.QueryRow(ctx, `SELECT owner_id::text FROM routes WHERE id = $1`, routeID).Scan(&routeOwner); e == nil && routeOwner == actor {
				err = nil
			}
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM collection_routes WHERE collection_id = $1 AND route_id = $2`, id, routeID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(apperr.NotFound, "route is not in this collection")
		}
		if _, err := tx.Exec(ctx, `UPDATE collections SET updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{
			ActorID: actor, OwnerID: l.OwnerID, Action: "collection.route_remove", SubjectType: "collection", SubjectID: id,
			Details: map[string]any{"route_id": routeID},
		})
	})
}

// List supports scopes mine, shared, editable (mine + shared with me as
// editor), public and all; optionally only collections containing routeID.
func (s *Store) List(ctx context.Context, actor, scope, routeID string, page paging.Params) (ListResult, error) {
	res := paging.Empty[Collection](page)
	if actor == "" && scope != "public" && scope != "all" {
		return res, apperr.New(apperr.Unauthorized, "log in to list your own or shared collections")
	}
	actorID := audit.NullID(actor)

	filter := sq.And{sq.Expr("collection_role(c.id, ?) IS NOT NULL", actorID)}
	switch scope {
	case "mine":
		filter = append(filter, sq.Eq{"c.owner_id": actorID})
	case "shared":
		filter = append(filter, sq.Expr(
			"EXISTS (SELECT 1 FROM collection_shares cs WHERE cs.collection_id = c.id AND cs.user_id = ?)", actorID))
	case "editable":
		filter = append(filter, sq.Expr("collection_role(c.id, ?) IN ('owner', 'editor')", actorID))
	case "public":
		filter = append(filter, sq.Eq{"c.visibility": "public"})
	}
	if routeID != "" {
		filter = append(filter, sq.Expr(
			"EXISTS (SELECT 1 FROM collection_routes cr WHERE cr.collection_id = c.id AND cr.route_id = ?)", routeID))
	}

	return db.ListPage(ctx, s.pool,
		db.SQL.Select("count(*)").From("collections c").Where(filter),
		selectCollections(actor).Where(filter).OrderBy("c.updated_at DESC", "c.id DESC"),
		page, scanCollection)
}

func (s *Store) Shares(ctx context.Context, actor, id string) ([]sharing.Share, error) {
	c, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if c.MyRole != "owner" {
		return nil, errOwnerOnly
	}
	return sharing.List(ctx, s.pool, sharing.Collections, id)
}

func (s *Store) PutShare(ctx context.Context, actor, id, email, role string) (sharing.Share, error) {
	var share sharing.Share
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		share, err = sharing.Put(ctx, tx, sharing.Collections, id, l.OwnerID, actor, email, role)
		return err
	})
	return share, err
}

// DeleteShare: the owner can remove anyone; users can remove themselves.
func (s *Store) DeleteShare(ctx context.Context, actor, id, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		l, err := lock(ctx, tx, actor, id, actor != userID)
		if err != nil && !(actor == userID && errors.Is(err, errReadOnly)) {
			return err
		}
		return sharing.Delete(ctx, tx, sharing.Collections, id, l.OwnerID, actor, userID)
	})
}
