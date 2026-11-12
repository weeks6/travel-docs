package collections

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/httpx"
)

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

func actorAndID(c *echo.Context) (string, string, error) {
	return httpx.ActorAndID(c, ErrNotFound)
}
