// Package events streams a user's real-time notifications (import job
// progress, trip invitations) as Server-Sent Events.
//
// The stream needs the usual Authorization header, so browsers read it with
// fetch() rather than EventSource. It ends when the access token expires or
// when the server can no longer guarantee delivery (event "resync"); the
// client should then reload its state and reconnect.
package events

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/metrics"
	"github.com/weeks6/travel-docs/pubsub"
	"github.com/weeks6/travel-docs/services/auth"
)

// Handler serves GET /events.
type Handler struct {
	Broker pubsub.Broker
	// Shutdown is closed when the server shuts down; open streams end then.
	Shutdown <-chan struct{}
	// Heartbeat is the interval of keep-alive comments. Default 20s.
	Heartbeat time.Duration
}

func (h *Handler) Register(e *echo.Echo) {
	e.GET("/events", h.stream)
}

func (h *Handler) stream(c *echo.Context) error {
	ctx := c.Request().Context()
	userID, err := auth.RequireUser(ctx)
	if err != nil {
		return err
	}
	metrics.MarkStreaming(c)

	// Subscribe before telling the client it is connected, so nothing
	// published after "ready" is missed.
	sub, err := h.Broker.Subscribe(ctx, pubsub.UserTopic(userID))
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		c.Logger().Error("subscribe to user events", "error", err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "live events are unavailable; try again later")
	}
	defer sub.Close()

	// The server's WriteTimeout would cut the stream.
	rc := http.NewResponseController(c.Response())
	rc.SetWriteDeadline(time.Time{})

	hdr := c.Response().Header()
	hdr.Set(echo.HeaderContentType, "text/event-stream")
	hdr.Set(echo.HeaderCacheControl, "no-store")
	hdr.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	c.Response().WriteHeader(http.StatusOK)

	w := c.Response()
	send := func(format string, args ...any) error {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return err
		}
		return rc.Flush()
	}
	if err := send("retry: 3000\nevent: ready\ndata: {}\n\n"); err != nil {
		return nil
	}

	interval := h.Heartbeat
	if interval <= 0 {
		interval = 20 * time.Second
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()

	var expired <-chan time.Time
	if exp := auth.TokenExpiry(ctx); !exp.IsZero() {
		t := time.NewTimer(time.Until(exp))
		defer t.Stop()
		expired = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-h.Shutdown:
			send("event: shutdown\ndata: {}\n\n")
			return nil
		case <-expired:
			send("event: reauth\ndata: {}\n\n")
			return nil
		case <-heartbeat.C:
			if err := send(": ping\n\n"); err != nil {
				return nil
			}
		case data, ok := <-sub.C:
			if !ok {
				send("event: resync\ndata: {}\n\n")
				return nil
			}
			// Events are single-line JSON.
			if err := send("data: %s\n\n", data); err != nil {
				return nil
			}
		}
	}
}
