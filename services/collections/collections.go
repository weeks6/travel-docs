// Package collections groups routes into collections that can be shared.
//
// Permissions:
//   - viewers (shared viewer, or anyone for public collections) can see the
//     collection and the routes their owners put in it;
//   - editors can rename it and add or remove routes;
//   - only the owner can change visibility, manage shares and delete it.
//
// A route can be added by a collection owner/editor only if they own the
// route or the route is public. Adding your own route makes it visible to
// everyone who can see the collection.
package collections

import (
	"context"
	"errors"
	"github.com/weeks6/travel-docs/internal/httpx"
	"github.com/weeks6/travel-docs/internal/paging"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/audit"
	"github.com/weeks6/travel-docs/db"
	"github.com/weeks6/travel-docs/services/auth"
	"github.com/weeks6/travel-docs/sharing"
)

var (
	ErrNotFound  = apperr.New(apperr.NotFound, "collection not found")
	errReadOnly  = apperr.New(apperr.Forbidden, "you can view this collection but not edit it")
	errOwnerOnly = apperr.New(apperr.Forbidden, "only the collection owner can do this")
)

type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	OwnerID     string    `json:"owner_id"`
	OwnerName   string    `json:"owner_name"`
	MyRole      string    `json:"my_role"`
	RouteCount  int       `json:"route_count"` // routes visible to the requester
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

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

// Input is used to create or update a collection; nil fields are unchanged.
type Input struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
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
	if in.Visibility != nil && *in.Visibility != "private" && *in.Visibility != "public" {
		return apperr.New(apperr.Invalid, "visibility must be private or public")
	}
	return nil
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

// ListResult is one page of collections.
type ListResult = paging.Page[Collection]

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

// Handler serves /collections.
type Handler struct {
	Store *Store
}

func (h *Handler) Register(e *echo.Echo) {
	small := middleware.BodyLimit(64 << 10)
	g := e.Group("/collections")
	g.POST("", h.create, small)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update, small)
	g.DELETE("/:id", h.delete)
	g.PUT("/:id/routes/:route_id", h.addRoute)
	g.DELETE("/:id/routes/:route_id", h.removeRoute)
	g.GET("/:id/shares", h.shares)
	g.PUT("/:id/shares", h.putShare, small)
	g.DELETE("/:id/shares/:user_id", h.deleteShare)
}

func (h *Handler) create(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	var in Input
	if err := echo.BindBody(c, &in); err != nil {
		return err
	}
	col, err := h.Store.Create(c.Request().Context(), actor, in)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderLocation, "/collections/"+col.ID)
	return c.JSON(http.StatusCreated, col)
}

// list: ?scope=mine|shared|editable|public|all (default mine, or public when
// anonymous), ?route_id= (only collections containing it), page, page_size.
func (h *Handler) list(c *echo.Context) error {
	actor := auth.UserID(c.Request().Context())
	q := c.QueryParams()
	page, err := httpx.ParsePage(q)
	if err != nil {
		return err
	}
	routeID, err := httpx.QueryUUID(q, "route_id")
	if err != nil {
		return err
	}
	scope := q.Get("scope")
	switch scope {
	case "":
		scope = "mine"
		if actor == "" {
			scope = "public"
		}
	case "mine", "shared", "editable", "public", "all":
	default:
		return apperr.New(apperr.Invalid, "scope must be one of mine, shared, editable, public, all")
	}
	res, err := h.Store.List(c.Request().Context(), actor, scope, routeID, page)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}

func (h *Handler) get(c *echo.Context) error {
	id, err := httpx.PathUUID(c, "id", ErrNotFound)
	if err != nil {
		return err
	}
	col, err := h.Store.Get(c.Request().Context(), auth.UserID(c.Request().Context()), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, col)
}

func (h *Handler) update(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	var in Input
	if err := echo.BindBody(c, &in); err != nil {
		return err
	}
	col, err := h.Store.Update(c.Request().Context(), actor, id, in)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, col)
}

func (h *Handler) delete(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	if err := h.Store.Delete(c.Request().Context(), actor, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) addRoute(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	routeID, err := httpx.PathUUID(c, "route_id", errRouteNotFound)
	if err != nil {
		return err
	}
	if err := h.Store.AddRoute(c.Request().Context(), actor, id, routeID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) removeRoute(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	routeID, err := httpx.PathUUID(c, "route_id", errRouteNotFound)
	if err != nil {
		return err
	}
	if err := h.Store.RemoveRoute(c.Request().Context(), actor, id, routeID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) shares(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	shares, err := h.Store.Shares(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": shares})
}

func (h *Handler) putShare(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	share, err := h.Store.PutShare(c.Request().Context(), actor, id, req.Email, req.Role)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, share)
}

func (h *Handler) deleteShare(c *echo.Context) error {
	actor, id, err := actorAndID(c)
	if err != nil {
		return err
	}
	userID, err := httpx.PathUUID(c, "user_id", apperr.New(apperr.NotFound, "share not found"))
	if err != nil {
		return err
	}
	if err := h.Store.DeleteShare(c.Request().Context(), actor, id, userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

var errRouteNotFound = apperr.New(apperr.NotFound, "route not found")

func actorAndID(c *echo.Context) (string, string, error) {
	return httpx.ActorAndID(c, ErrNotFound)
}
