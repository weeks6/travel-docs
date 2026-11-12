// Package routes implements GPX route upload, storage, editing, version
// history and sharing.
package routes

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/gpx"
	"github.com/weeks6/travel-docs/internal/httpx"
)

const (
	// MaxUploadSize limits GPX uploads and GeoJSON edits, in bytes.
	MaxUploadSize = 20 << 20
	maxJSONSize   = 64 << 10
)

// Handler serves the /routes API.
type Handler struct {
	Store *Store
}

// Register adds the route endpoints.
func (h *Handler) Register(e *echo.Echo) {
	upload := middleware.BodyLimit(MaxUploadSize)
	small := middleware.BodyLimit(maxJSONSize)

	g := e.Group("/routes")
	g.POST("", h.upload, upload)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update, small)
	g.DELETE("/:id", h.delete)
	g.GET("/:id/geometry", h.geometry)
	g.PUT("/:id/geometry", h.replaceGeometry, upload)
	g.GET("/:id/elevation", h.elevation)
	g.GET("/:id/versions", h.versions)
	g.GET("/:id/versions/:version/geometry", h.versionGeometry)
	g.POST("/:id/versions/:version/restore", h.restore)
	g.GET("/:id/shares", h.shares)
	g.PUT("/:id/shares", h.putShare, small)
	g.DELETE("/:id/shares/:user_id", h.deleteShare)
}

// upload accepts multipart/form-data with a "file" field (the GPX file) and
// optional "name" and "visibility" (private by default) fields.
func (h *Handler) upload(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	form, err := ParseNewRouteForm(c.FormValue("name"), c.FormValue("visibility"))
	if err != nil {
		return err
	}
	f, filename, err := readGPXUpload(c)
	if err != nil {
		return err
	}
	content, err := BuildContent(f, filename, form.Name)
	if err != nil {
		return err
	}
	route, err := h.Store.Create(c.Request().Context(), actor, content, form.Visibility)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderLocation, "/routes/"+route.ID)
	return writeRoute(c, http.StatusCreated, route)
}

// readGPXUpload parses the multipart "file" field as GPX.
func readGPXUpload(c *echo.Context) (*gpx.File, string, error) {
	header, err := c.FormFile("file")
	if err != nil {
		if echo.StatusCode(err) != 0 {
			return nil, "", err // e.g. body over the size limit (413)
		}
		return nil, "", apperr.New(apperr.Invalid, `expected multipart/form-data with a "file" field`)
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	f, err := ParseGPX(file)
	if err != nil {
		if apperr.KindOf(err) != apperr.Internal {
			return nil, "", err
		}
		return nil, "", apperr.New(apperr.Invalid, "could not read upload")
	}
	return f, header.Filename, nil
}

func (h *Handler) list(c *echo.Context) error {
	actor := auth.UserID(c.Request().Context())
	params, err := ParseListParams(c.QueryParams(), actor != "")
	if err != nil {
		return err
	}
	res, err := h.Store.List(c.Request().Context(), actor, params)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}

// ParseListParams reads pagination, filter and sort options:
//
//	page, page_size, scope=mine|shared|public|all, collection_id, name,
//	min_distance_m, max_distance_m, min_ascent_m, max_ascent_m,
//	bbox=minLon,minLat,maxLon,maxLat,
//	sort=created_at|updated_at|name|distance|ascent, order=asc|desc
//
// The default scope is "mine" for logged-in users and "public" otherwise,
// or "all" when listing a collection.
func ParseListParams(q url.Values, loggedIn bool) (ListParams, error) {
	p := ListParams{Sort: SortCreatedAt, Desc: true}
	bad := func(msg string) (ListParams, error) { return p, apperr.New(apperr.Invalid, msg) }

	page, err := httpx.ParsePage(q)
	if err != nil {
		return p, err
	}
	p.Page, p.PageSize = page.Page, page.PageSize
	if p.CollectionID, err = httpx.QueryUUID(q, "collection_id"); err != nil {
		return p, err
	}
	switch s := Scope(q.Get("scope")); s {
	case "":
		switch {
		case p.CollectionID != "":
			p.Scope = ScopeAll
		case loggedIn:
			p.Scope = ScopeMine
		default:
			p.Scope = ScopePublic
		}
	case ScopeMine, ScopeShared, ScopePublic, ScopeAll:
		p.Scope = s
	default:
		return bad("scope must be one of mine, shared, public, all")
	}

	p.Name = strings.TrimSpace(q.Get("name"))
	for _, f := range []struct {
		key string
		dst **float64
	}{
		{"min_distance_m", &p.MinDistanceM},
		{"max_distance_m", &p.MaxDistanceM},
		{"min_ascent_m", &p.MinAscentM},
		{"max_ascent_m", &p.MaxAscentM},
	} {
		if v := q.Get(f.key); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n < 0 {
				return bad(f.key + " must be a non-negative number")
			}
			*f.dst = &n
		}
	}

	if v := q.Get("bbox"); v != "" {
		b, err := parseBBox(v)
		if err != nil {
			return p, err
		}
		p.BBox = &b
	}

	if v := q.Get("sort"); v != "" {
		switch s := SortField(v); s {
		case SortCreatedAt, SortUpdatedAt, SortName, SortDistance, SortAscent:
			p.Sort = s
		default:
			return bad("sort must be one of created_at, updated_at, name, distance, ascent")
		}
	}
	switch q.Get("order") {
	case "", "desc":
		p.Desc = true
	case "asc":
		p.Desc = false
	default:
		return bad("order must be asc or desc")
	}
	return p, nil
}

func parseBBox(v string) (BBox, error) {
	errBBox := apperr.New(apperr.Invalid, "bbox must be minLon,minLat,maxLon,maxLat")
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return BBox{}, errBBox
	}
	var n [4]float64
	for i, s := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return BBox{}, errBBox
		}
		n[i] = f
	}
	b := BBox{MinLon: n[0], MinLat: n[1], MaxLon: n[2], MaxLat: n[3]}
	if b.MinLon > b.MaxLon || b.MinLat > b.MaxLat ||
		b.MinLon < -180 || b.MaxLon > 180 || b.MinLat < -90 || b.MaxLat > 90 {
		return BBox{}, errBBox
	}
	return b, nil
}

func (h *Handler) get(c *echo.Context) error {
	id, err := routeID(c)
	if err != nil {
		return err
	}
	route, err := h.Store.Get(c.Request().Context(), auth.UserID(c.Request().Context()), id)
	if err != nil {
		return err
	}
	return writeRoute(c, http.StatusOK, route)
}

// update applies a JSON MetadataUpdate. Send If-Match with the version from
// the ETag to avoid overwriting someone else's change.
func (h *Handler) update(c *echo.Context) error {
	actor, id, ifVersion, err := editRequest(c)
	if err != nil {
		return err
	}
	var upd MetadataUpdate
	if err := echo.BindBody(c, &upd); err != nil {
		return err
	}
	route, err := h.Store.UpdateMetadata(c.Request().Context(), actor, id, upd, ifVersion)
	if err != nil {
		return err
	}
	return writeRoute(c, http.StatusOK, route)
}

// replaceGeometry accepts either a new GPX file (multipart/form-data, field
// "file") or GeoJSON (application/json or application/geo+json).
func (h *Handler) replaceGeometry(c *echo.Context) error {
	actor, id, ifVersion, err := editRequest(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Check access before reading a potentially large body.
	if _, err := h.Store.CheckEditable(ctx, actor, id, ifVersion); err != nil {
		return err
	}

	mediaType, _, _ := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	var f *gpx.File
	var filename string
	geoJSON := false
	switch mediaType {
	case echo.MIMEMultipartForm:
		if f, filename, err = readGPXUpload(c); err != nil {
			return err
		}
	case echo.MIMEApplicationJSON, "application/geo+json":
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err // over the size limit
		}
		if f, err = ParseGeoJSON(body); err != nil {
			return err
		}
		geoJSON = true
	default:
		return apperr.New(apperr.Invalid, "send a GPX file as multipart/form-data or GeoJSON as application/geo+json")
	}

	content, err := BuildContent(f, filename, "")
	if err != nil {
		return err
	}
	route, err := h.Store.ReplaceGeometry(ctx, actor, id, content, geoJSON, ifVersion)
	if err != nil {
		return err
	}
	return writeRoute(c, http.StatusOK, route)
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

// geometry returns the route as a GeoJSON Feature, ready for map libraries.
func (h *Handler) geometry(c *echo.Context) error {
	id, err := routeID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	actor := auth.UserID(ctx)
	route, err := h.Store.Get(ctx, actor, id)
	if err != nil {
		return err
	}
	geom, err := h.Store.Geometry(ctx, actor, id)
	if err != nil {
		return err
	}
	return writeFeature(c, geom, map[string]any{
		"id": route.ID, "name": route.Name, "version": route.Version, "distance_m": route.DistanceM,
	})
}

func (h *Handler) elevation(c *echo.Context) error {
	id, err := routeID(c)
	if err != nil {
		return err
	}
	profile, err := h.Store.ElevationProfile(c.Request().Context(), auth.UserID(c.Request().Context()), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"points": profile})
}

func (h *Handler) versions(c *echo.Context) error {
	id, err := routeID(c)
	if err != nil {
		return err
	}
	versions, err := h.Store.Versions(c.Request().Context(), auth.UserID(c.Request().Context()), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": versions})
}

func (h *Handler) versionGeometry(c *echo.Context) error {
	id, err := routeID(c)
	if err != nil {
		return err
	}
	version, err := versionParam(c)
	if err != nil {
		return err
	}
	geom, err := h.Store.VersionGeometry(c.Request().Context(), auth.UserID(c.Request().Context()), id, version)
	if err != nil {
		return err
	}
	return writeFeature(c, geom, map[string]any{"id": id, "version": version})
}

func (h *Handler) restore(c *echo.Context) error {
	actor, id, ifVersion, err := editRequest(c)
	if err != nil {
		return err
	}
	version, err := versionParam(c)
	if err != nil {
		return err
	}
	route, err := h.Store.Restore(c.Request().Context(), actor, id, version, ifVersion)
	if err != nil {
		return err
	}
	return writeRoute(c, http.StatusOK, route)
}

func (h *Handler) shares(c *echo.Context) error {
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
	if err != nil {
		return err
	}
	shares, err := h.Store.Shares(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": shares})
}

// putShare takes {"email": "...", "role": "viewer"|"editor"}.
func (h *Handler) putShare(c *echo.Context) error {
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
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
	actor, id, err := httpx.ActorAndID(c, ErrNotFound)
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

// routeID validates the :id path parameter.
func routeID(c *echo.Context) (string, error) { return httpx.PathUUID(c, "id", ErrNotFound) }

// editRequest extracts what every editing endpoint needs.
func editRequest(c *echo.Context) (actor, id string, ifVersion int, err error) {
	if actor, id, err = httpx.ActorAndID(c, ErrNotFound); err != nil {
		return
	}
	ifVersion, err = httpx.IfMatchVersion(c.Request().Header.Get("If-Match"))
	return
}

func versionParam(c *echo.Context) (int, error) {
	v, err := strconv.Atoi(c.Param("version"))
	if err != nil || v < 1 {
		return 0, apperr.New(apperr.NotFound, "version not found")
	}
	return v, nil
}

func writeRoute(c *echo.Context, status int, route Route) error {
	c.Response().Header().Set("ETag", httpx.VersionETag(route.Version))
	return c.JSON(status, route)
}

func writeFeature(c *echo.Context, geom json.RawMessage, props map[string]any) error {
	b, err := json.Marshal(map[string]any{"type": "Feature", "geometry": geom, "properties": props})
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, "application/geo+json", b)
}
