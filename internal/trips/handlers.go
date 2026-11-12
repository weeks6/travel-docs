package trips

import (
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/httpx"
	"github.com/weeks6/travel-docs/internal/routes"
)

const maxJSONBody = 64 << 10

// Handler serves the /trips API.
type Handler struct {
	Store *Store
}

func (h *Handler) Register(e *echo.Echo) {
	g := e.Group("/trips")
	g.POST("", h.create, middleware.BodyLimit(routes.MaxUploadSize))
	g.GET("", h.list)
	g.GET("/stats", h.stats)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update, middleware.BodyLimit(maxJSONBody))
	g.DELETE("/:id", h.delete)
}

// create logs a trip, either on an existing route (JSON Input with
// route_id) or on a GPX file uploaded with it (multipart/form-data: "file",
// and optional "name", "notes", "date", "duration_s" and "visibility" of
// the new route, private by default).
func (h *Handler) create(c *echo.Context) error {
	ctx := c.Request().Context()
	actor, err := auth.RequireUser(ctx)
	if err != nil {
		return err
	}
	var trip Trip
	mediaType, _, _ := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if mediaType == echo.MIMEMultipartForm {
		trip, err = h.createWithUpload(c, actor)
	} else {
		var in Input
		if err := echo.BindBody(c, &in); err != nil {
			return err
		}
		trip, err = h.Store.Create(ctx, actor, in)
	}
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderLocation, "/trips/"+trip.ID)
	return c.JSON(http.StatusCreated, trip)
}

func (h *Handler) createWithUpload(c *echo.Context, actor string) (Trip, error) {
	form, err := routes.ParseNewRouteForm(c.FormValue("name"), c.FormValue("visibility"))
	if err != nil {
		return Trip{}, err
	}
	in := Input{}
	if form.Name != "" {
		in.Name = &form.Name
	}
	if v := c.FormValue("notes"); v != "" {
		in.Notes = &v
	}
	if v := c.FormValue("date"); v != "" {
		in.Date = &v
	}
	if v := strings.TrimSpace(c.FormValue("duration_s")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Trip{}, apperr.New(apperr.Invalid, "duration_s must be a whole number of seconds")
		}
		in.DurationS = &n
	}
	f, filename, err := routes.ReadGPXUpload(c)
	if err != nil {
		return Trip{}, err
	}
	content, err := routes.BuildContent(f, filename, form.Name)
	if err != nil {
		return Trip{}, err
	}
	return h.Store.CreateWithRoute(c.Request().Context(), actor, content, form.Visibility, in)
}

// filter reads ?year and ?route_id.
func filter(c *echo.Context) (Filter, error) {
	var f Filter
	q := c.QueryParams()
	if v := q.Get("year"); v != "" {
		y, err := strconv.Atoi(v)
		if err != nil || y < 1900 || y > 9999 {
			return f, apperr.New(apperr.Invalid, "year must be a year such as 2026")
		}
		f.Year = y
	}
	var err error
	f.RouteID, err = httpx.QueryUUID(q, "route_id")
	return f, err
}

// list supports page, page_size, year and route_id.
func (h *Handler) list(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	page, err := httpx.ParsePage(c.QueryParams())
	if err != nil {
		return err
	}
	f, err := filter(c)
	if err != nil {
		return err
	}
	res, err := h.Store.List(c.Request().Context(), actor, f, page)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}

// stats sums up trips; supports year and route_id.
func (h *Handler) stats(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	f, err := filter(c)
	if err != nil {
		return err
	}
	st, err := h.Store.Stats(c.Request().Context(), actor, f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, st)
}

func (h *Handler) get(c *echo.Context) error {
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
	if err != nil {
		return err
	}
	trip, err := h.Store.Get(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, trip)
}

func (h *Handler) update(c *echo.Context) error {
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
	if err != nil {
		return err
	}
	var in Input
	if err := echo.BindBody(c, &in); err != nil {
		return err
	}
	trip, err := h.Store.Update(c.Request().Context(), actor, id, in)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, trip)
}

func (h *Handler) delete(c *echo.Context) error {
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
	if err != nil {
		return err
	}
	if err := h.Store.Delete(c.Request().Context(), actor, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
