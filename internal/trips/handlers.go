package trips

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/httpx"
	"github.com/weeks6/travel-docs/internal/metrics"
	"github.com/weeks6/travel-docs/internal/pubsub"
)

const (
	defaultHistoryPoints    = 2000
	maxHistoryPoints        = 10000
	maxJSONBody             = 64 << 10
	maxLocationsRequestBody = 64 << 10
)

// Handler serves the /trips and /trip-invitations APIs.
type Handler struct {
	Store  *Store
	Hub    *Hub
	Broker pubsub.Broker
	Log    *slog.Logger
}

func (h *Handler) Register(e *echo.Echo) {
	small := middleware.BodyLimit(maxJSONBody)
	g := e.Group("/trips")
	g.POST("", h.create, small)
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update, small)
	g.DELETE("/:id", h.delete)
	g.POST("/:id/start", h.start)
	g.POST("/:id/end", h.end)
	g.GET("/:id/participants", h.participants)
	g.DELETE("/:id/participants/:user_id", h.removeParticipant)
	g.GET("/:id/invitations", h.tripInvitations)
	g.POST("/:id/invitations", h.invite, small)
	g.DELETE("/:id/invitations/:invitation_id", h.revokeInvitation)
	g.POST("/:id/locations", h.addLocations, middleware.BodyLimit(maxLocationsRequestBody))
	g.GET("/:id/history", h.history)
	g.GET("/:id/ws", h.Hub.serve)

	inv := e.Group("/trip-invitations")
	inv.GET("", h.myInvitations)
	inv.POST("/:id/accept", func(c *echo.Context) error { return h.respond(c, true) })
	inv.POST("/:id/decline", func(c *echo.Context) error { return h.respond(c, false) })
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
	t, err := h.Store.Create(c.Request().Context(), actor, in)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderLocation, "/trips/"+t.ID)
	return c.JSON(http.StatusCreated, t)
}

// list supports ?status=planned|active|ended, page and page_size.
func (h *Handler) list(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	status := c.QueryParam("status")
	switch status {
	case "", StatusPlanned, StatusActive, StatusEnded:
	case "all":
		status = ""
	default:
		return apperr.New(apperr.Invalid, "status must be planned, active, ended or all")
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
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	t, err := h.Store.Get(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, t)
}

func (h *Handler) update(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	var in Input
	if err := echo.BindBody(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	t, err := h.Store.Update(ctx, actor, id, in)
	if err != nil {
		return err
	}
	h.publish(ctx, id, ChangeEvent{Type: "trip"})
	return c.JSON(http.StatusOK, t)
}

func (h *Handler) delete(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := h.Store.Delete(ctx, actor, id); err != nil {
		return err
	}
	h.publish(ctx, id, ChangeEvent{Type: "deleted"})
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) start(c *echo.Context) error { return h.setStatus(c, StatusActive) }

func (h *Handler) end(c *echo.Context) error { return h.setStatus(c, StatusEnded) }

func (h *Handler) setStatus(c *echo.Context, to string) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	t, err := h.Store.SetStatus(ctx, actor, id, to)
	if err != nil {
		return err
	}
	h.publish(ctx, id, ChangeEvent{Type: "trip"})
	return c.JSON(http.StatusOK, t)
}

func (h *Handler) participants(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	list, err := h.Store.Participants(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) removeParticipant(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	userID, err := httpx.PathUUID(c, "user_id", apperr.New(apperr.NotFound, "participant not found"))
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	if err := h.Store.RemoveParticipant(ctx, actor, id, userID); err != nil {
		return err
	}
	h.publish(ctx, id, ChangeEvent{Type: "removed", UserID: userID})
	h.publish(ctx, id, ChangeEvent{Type: "participants"})
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) tripInvitations(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	list, err := h.Store.TripInvitations(c.Request().Context(), actor, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}

// invite takes {"email": "..."} of a registered user.
func (h *Handler) invite(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	ctx := c.Request().Context()
	inv, err := h.Store.Invite(ctx, actor, id, req.Email)
	if err != nil {
		return err
	}
	h.notifyUser(ctx, inv.InviteeID, inv)
	return c.JSON(http.StatusCreated, inv)
}

func (h *Handler) revokeInvitation(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	invID, err := httpx.PathUUID(c, "invitation_id", errInvitationGone)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	inv, err := h.Store.RevokeInvitation(ctx, actor, id, invID)
	if err != nil {
		return err
	}
	h.notifyUser(ctx, inv.InviteeID, inv)
	return c.NoContent(http.StatusNoContent)
}

// myInvitations lists invitations sent to me: ?status=pending (default) or all.
func (h *Handler) myInvitations(c *echo.Context) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	all := false
	switch c.QueryParam("status") {
	case "", "pending":
	case "all":
		all = true
	default:
		return apperr.New(apperr.Invalid, "status must be pending or all")
	}
	list, err := h.Store.MyInvitations(c.Request().Context(), actor, all)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": list})
}

func (h *Handler) respond(c *echo.Context, accept bool) error {
	actor, err := auth.RequireUser(c.Request().Context())
	if err != nil {
		return err
	}
	invID, err := httpx.PathUUID(c, "id", errInvitationGone)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	inv, err := h.Store.RespondInvitation(ctx, actor, invID, accept)
	if err != nil {
		return err
	}
	if accept {
		h.publish(ctx, inv.TripID, ChangeEvent{Type: "participants"})
	}
	if inv.InvitedByID != nil {
		h.notifyUser(ctx, *inv.InvitedByID, inv)
	}
	h.notifyUser(ctx, inv.InviteeID, inv) // the invitee's other devices
	return c.JSON(http.StatusOK, inv)
}

// addLocations takes {"points": [{"lat", "lon", "ele"?, "accuracy_m"?,
// "speed_mps"?, "heading_deg"?, "recorded_at"?}, ...]} (at most 100), for
// clients that can't keep a WebSocket open or send buffered points later.
func (h *Handler) addLocations(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	var req struct {
		Points []Position `json:"points"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	if len(req.Points) == 0 || len(req.Points) > MaxBatchPoints {
		return apperr.Newf(apperr.Invalid, "send between 1 and %d points", MaxBatchPoints)
	}
	ctx := c.Request().Context()
	newest, err := h.Store.AddLocations(ctx, id, actor, req.Points)
	if err != nil {
		return err
	}
	metrics.LocationUpdates.WithLabelValues("http").Add(float64(len(req.Points)))
	if newest != nil {
		h.publish(ctx, id, LocationEvent{Type: "location", UserID: actor, Position: *newest})
	}
	return c.JSON(http.StatusAccepted, map[string]any{"accepted": len(req.Points)})
}

// history supports user_id, since and until (RFC 3339) and max_points
// (per participant, default 2000).
func (h *Handler) history(c *echo.Context) error {
	actor, id, err := actorAndTrip(c)
	if err != nil {
		return err
	}
	p := HistoryParams{MaxPoints: defaultHistoryPoints}
	if p.UserID, err = httpx.QueryUUID(c.QueryParams(), "user_id"); err != nil {
		return err
	}
	for _, f := range []struct {
		key string
		dst **time.Time
	}{{"since", &p.Since}, {"until", &p.Until}} {
		if v := c.QueryParam(f.key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return apperr.Newf(apperr.Invalid, "%s must be an RFC 3339 time", f.key)
			}
			*f.dst = &t
		}
	}
	if err := echo.QueryParamsBinder(c).Int("max_points", &p.MaxPoints).BindError(); err != nil ||
		p.MaxPoints < 2 || p.MaxPoints > maxHistoryPoints {
		return apperr.Newf(apperr.Invalid, "max_points must be between 2 and %d", maxHistoryPoints)
	}
	fc, err := h.Store.History(c.Request().Context(), actor, id, p)
	if err != nil {
		return err
	}
	b, err := json.Marshal(fc)
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, "application/geo+json", b)
}

// publish sends a live event to the trip's WebSocket clients.
func (h *Handler) publish(ctx context.Context, tripID string, ev any) { h.Hub.publish(ctx, tripID, ev) }

// notifyUser sends an invitation update to a user's event stream.
func (h *Handler) notifyUser(ctx context.Context, userID string, inv Invitation) {
	data, err := json.Marshal(UserEvent{Type: "invitation", Invitation: inv})
	if err == nil {
		err = h.Broker.Publish(ctx, pubsub.UserTopic(userID), data)
	}
	if err != nil {
		h.Log.Warn("publish invitation event", "user_id", userID, "error", err)
	}
}

func actorAndTrip(c *echo.Context) (actor, id string, err error) {
	return httpx.ActorAndID(c, ErrNotFound)
}
