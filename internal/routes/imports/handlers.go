package imports

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/httpx"
	"github.com/weeks6/travel-docs/internal/metrics"
	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/routes"
)

const (
	// MaxImportSize is the largest GPX file accepted for background import.
	MaxImportSize = 50 << 20
	// multipartOverhead leaves room for the other form fields.
	multipartOverhead   = 1 << 20
	maxIdempotencyKey   = 255
	uploadSlotWait      = 30 * time.Second
	DefaultUploadSlots  = 4
	idempotentReplayHdr = "Idempotent-Replayed"
)

// Handler serves the /import-jobs API.
type Handler struct {
	Store  *Store
	Routes *routes.Store
	Broker pubsub.Broker
	Log    *slog.Logger
	// slots limits how many uploads are read into memory at once.
	slots chan struct{}
}

// NewHandler returns a handler accepting up to uploadSlots concurrent uploads.
func NewHandler(store *Store, routeStore *routes.Store, broker pubsub.Broker, log *slog.Logger, uploadSlots int) *Handler {
	if uploadSlots <= 0 {
		uploadSlots = DefaultUploadSlots
	}
	return &Handler{Store: store, Routes: routeStore, Broker: broker, Log: log, slots: make(chan struct{}, uploadSlots)}
}

func (h *Handler) Register(e *echo.Echo) {
	g := e.Group("/import-jobs")
	g.POST("", h.create, middleware.BodyLimit(MaxImportSize+multipartOverhead))
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/cancel", h.cancel)
	g.POST("/:id/retry", h.retry)
}

// create queues a GPX file for import. multipart/form-data fields:
//
//	file        the GPX file (required)
//	route_id    import as a new version of this route instead of a new route
//	name        name of the new route (optional)
//	visibility  private (default) or public, for new routes
//
// Headers: Idempotency-Key (recommended) and, with route_id, If-Match.
// Responds 202 with the job, or 200 with the existing job when the
// Idempotency-Key was already used for the same upload.
func (h *Handler) create(c *echo.Context) error {
	ctx := c.Request().Context()
	actor, err := auth.RequireUser(ctx)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(c.Request().Header.Get("Idempotency-Key"))
	if len(key) > maxIdempotencyKey || !printableASCII(key) {
		return apperr.Newf(apperr.Invalid, "Idempotency-Key must be at most %d printable ASCII characters", maxIdempotencyKey)
	}

	// Bound the memory used by uploads being read at the same time.
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(uploadSlotWait):
		c.Response().Header().Set("Retry-After", "10")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "the server is busy with other uploads; try again shortly")
	}

	n := NewJob{OwnerID: actor, Kind: KindCreate, IdempotencyKey: key}
	if v := c.FormValue("route_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return routes.ErrNotFound
		}
		if n.IfVersion, err = httpx.IfMatchVersion(c.Request().Header.Get("If-Match")); err != nil {
			return err
		}
		// Check access now so the user gets a clear error right away.
		r, err := h.Routes.CheckEditable(ctx, actor, id.String(), n.IfVersion)
		if err != nil {
			return err
		}
		n.Kind, n.RouteID, n.Visibility = KindReplaceGeometry, r.ID, routes.VisibilityPrivate
	} else {
		form, err := routes.ParseNewRouteForm(c.FormValue("name"), c.FormValue("visibility"))
		if err != nil {
			return err
		}
		n.Name, n.Visibility = form.Name, form.Visibility
	}

	header, err := c.FormFile("file")
	if err != nil {
		if echo.StatusCode(err) != 0 {
			return err // e.g. body over the size limit (413)
		}
		return apperr.New(apperr.Invalid, `expected multipart/form-data with a "file" field`)
	}
	if header.Size > MaxImportSize {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge,
			"the file is larger than "+strconv.Itoa(MaxImportSize>>20)+" MB")
	}
	f, err := header.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	if n.Payload, err = io.ReadAll(io.LimitReader(f, MaxImportSize+1)); err != nil {
		return apperr.New(apperr.Invalid, "could not read upload")
	}
	if len(n.Payload) > MaxImportSize {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge,
			"the file is larger than "+strconv.Itoa(MaxImportSize>>20)+" MB")
	}
	if !routes.LooksLikeXML(n.Payload) {
		return routes.ErrNotXML
	}
	n.Filename = routes.CleanFilename(header.Filename)
	metrics.ImportUploadBytes.Observe(float64(len(n.Payload)))

	job, replayed, err := h.Store.Enqueue(ctx, n)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderLocation, "/import-jobs/"+job.ID)
	if replayed {
		c.Response().Header().Set(idempotentReplayHdr, "true")
		return c.JSON(http.StatusOK, job)
	}
	Publish(ctx, h.Broker, h.Log, job)
	h.control(ctx, control{Op: "wake"})
	return c.JSON(http.StatusAccepted, job)
}

// control sends a message to the workers of every instance. If it is
// lost, workers still find the job when they next poll.
func (h *Handler) control(ctx context.Context, m control) {
	pubsub.Notify(ctx, h.Broker, h.Log, ControlTopic, m)
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// list supports ?status=active|finished|all, page and page_size.
func (h *Handler) list(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	status := c.QueryParam("status")
	switch status {
	case "", "all":
		status = ""
	case "active", "finished":
	default:
		return apperr.New(apperr.Invalid, "status must be active, finished or all")
	}
	page, err := httpx.ParsePage(c.QueryParams())
	if err != nil {
		return err
	}
	res, err := h.Store.List(c.Request().Context(), actor, status, page)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, res)
}

func (h *Handler) get(c *echo.Context) error {
	actor, id, err := h.request(c)
	if err != nil {
		return err
	}
	job, err := h.Store.Get(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, job)
}

func (h *Handler) cancel(c *echo.Context) error {
	actor, id, err := h.request(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	job, err := h.Store.Cancel(ctx, actor, id)
	if err != nil {
		return err
	}
	if job.Status == StatusRunning {
		// Stop it right away wherever it runs (the heartbeat would notice too).
		h.control(ctx, control{Op: "cancel", ID: job.ID})
	}
	Publish(ctx, h.Broker, h.Log, job)
	return c.JSON(http.StatusOK, job)
}

func (h *Handler) retry(c *echo.Context) error {
	actor, id, err := h.request(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	job, err := h.Store.Retry(ctx, actor, id)
	if err != nil {
		return err
	}
	Publish(ctx, h.Broker, h.Log, job)
	h.control(ctx, control{Op: "wake"})
	return c.JSON(http.StatusOK, job)
}

func (h *Handler) delete(c *echo.Context) error {
	actor, id, err := h.request(c)
	if err != nil {
		return err
	}
	if err := h.Store.Delete(c.Request().Context(), actor, id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) request(c *echo.Context) (actor, id string, err error) {
	return httpx.ActorAndID(c, ErrNotFound)
}
