package trips

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/internal/httpx"
	"github.com/weeks6/travel-docs/metrics"
	"github.com/weeks6/travel-docs/pubsub"
	"github.com/weeks6/travel-docs/ratelimit"
)

// WebSocket close codes used by the server (4000–4999 are application codes).
const (
	CloseUnauthorized websocket.StatusCode = 4001 // missing, invalid or expired token: refresh and reconnect
	CloseForbidden    websocket.StatusCode = 4003 // removed from the trip: don't reconnect
	CloseNotFound     websocket.StatusCode = 4004 // trip deleted or not visible: don't reconnect
	CloseResync       websocket.StatusCode = 4009 // events may have been missed: reconnect to get a fresh snapshot
)

const (
	authTimeout   = 10 * time.Second
	writeTimeout  = 10 * time.Second
	pingInterval  = 30 * time.Second
	maxMessageLen = 4096
)

// Authenticator verifies access tokens (implemented by auth.Service).
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (userID string, expires time.Time, err error)
}

// Hub serves trip WebSocket connections on this instance.
type Hub struct {
	store  *Store
	auth   Authenticator
	broker pubsub.Broker
	log    *slog.Logger
	// limiter limits location messages per trip participant.
	limiter *ratelimit.Limiter
	accept  *websocket.AcceptOptions

	shutdown     chan struct{}
	shutdownOnce sync.Once
	wg           sync.WaitGroup

	mu    sync.Mutex
	conns map[presenceKey]int // open connections per trip participant
}

type presenceKey struct{ trip, user string }

// NewHub returns a hub. originPatterns lists the browser origins
// (host[:port] patterns) allowed to connect; "*" allows any.
func NewHub(store *Store, auth Authenticator, broker pubsub.Broker, log *slog.Logger,
	limiter *ratelimit.Limiter, originPatterns []string) *Hub {
	opts := &websocket.AcceptOptions{OriginPatterns: originPatterns}
	for _, p := range originPatterns {
		if p == "*" {
			// Authentication uses a token in the first message, never
			// cookies, so cross-site connections can't act as the user.
			opts = &websocket.AcceptOptions{InsecureSkipVerify: true}
		}
	}
	return &Hub{
		store: store, auth: auth, broker: broker, log: log.With("component", "trip_ws"),
		limiter: limiter, accept: opts,
		shutdown: make(chan struct{}),
		conns:    map[presenceKey]int{},
	}
}

// Shutdown closes all connections (code 1001, going away) and waits until
// they have finished, or ctx ends.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.shutdownOnce.Do(func() { close(h.shutdown) })
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// clientMessage is any message a client sends.
type clientMessage struct {
	Type  string `json:"type"` // auth, location, ping
	Token string `json:"token,omitempty"`
	Position
}

type errorMessage struct {
	Type  string `json:"type"` // "error"
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// readyMessage is the first message after authentication: the current
// state; live events follow.
type readyMessage struct {
	Type         string        `json:"type"` // "ready"
	UserID       string        `json:"user_id"`
	Trip         Trip          `json:"trip"`
	Participants []Participant `json:"participants"`
}

// serve handles GET /trips/:id/ws. Protocol (JSON text messages):
//
//	client → {"type":"auth","token":"<access token>"}            first message, within 10 s
//	server → {"type":"ready","user_id",...,"trip":{...},"participants":[...]}
//	client → {"type":"location","lat":..,"lon":..,"ele"?,"accuracy_m"?,"speed_mps"?,"heading_deg"?,"recorded_at"?}
//	client → {"type":"auth","token":"..."}                       send a refreshed token before the old one expires
//	client → {"type":"ping"}                                     server → {"type":"pong"}
//	server → location, presence, trip, participants, removed, deleted and error events
func (h *Hub) serve(c *echo.Context) error {
	tripID, err := httpx.PathUUID(c, "id", ErrNotFound)
	if err != nil {
		return err
	}
	metrics.MarkStreaming(c)

	select {
	case <-h.shutdown:
		return echo.NewHTTPError(http.StatusServiceUnavailable, "server is shutting down")
	default:
	}
	h.wg.Add(1)
	defer h.wg.Done()

	// The server's read/write timeouts would cut the connection.
	rc := http.NewResponseController(c.Response())
	rc.SetReadDeadline(time.Time{})
	rc.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(c.Response(), c.Request(), h.accept)
	if err != nil {
		return nil // Accept already wrote the error response
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxMessageLen)
	metrics.WSConnections.Inc()
	defer metrics.WSConnections.Dec()

	// Not the request context: the hijacked connection outlives it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.Request().Context()))
	defer cancel()

	s := &session{hub: h, conn: conn, tripID: tripID, log: h.log.With("trip_id", tripID)}
	code, reason := s.run(ctx)
	conn.Close(code, reason)
	return nil
}

// session is one WebSocket connection.
type session struct {
	hub    *Hub
	conn   *websocket.Conn
	tripID string
	userID string
	log    *slog.Logger

	mu      sync.Mutex
	expires time.Time
}

func (s *session) run(ctx context.Context) (websocket.StatusCode, string) {
	h := s.hub
	// 1. Authenticate with the first message.
	actx, acancel := context.WithTimeout(ctx, authTimeout)
	_, data, err := s.conn.Read(actx)
	acancel()
	if err != nil {
		return CloseUnauthorized, "send {\"type\":\"auth\",\"token\":...} first"
	}
	var first clientMessage
	if json.Unmarshal(data, &first) != nil || first.Type != "auth" || first.Token == "" {
		return CloseUnauthorized, "the first message must be {\"type\":\"auth\",\"token\":...}"
	}
	userID, expires, err := h.auth.Authenticate(ctx, first.Token)
	if err != nil {
		if apperr.KindOf(err) == apperr.Unauthorized {
			return CloseUnauthorized, "invalid or expired access token"
		}
		s.log.Error("authenticate websocket", "error", err)
		return websocket.StatusInternalError, "internal error"
	}
	s.userID, s.expires = userID, expires
	s.log = s.log.With("user_id", userID)

	// 2. Subscribe before loading the snapshot so no event falls between.
	sub, err := h.broker.Subscribe(ctx, pubsub.TripTopic(s.tripID))
	if err != nil {
		s.log.Error("subscribe to trip events", "error", err)
		return websocket.StatusTryAgainLater, "live events are unavailable; reconnect later"
	}
	defer sub.Close()

	trip, err := h.store.Get(ctx, userID, s.tripID)
	if err != nil {
		if apperr.KindOf(err) == apperr.NotFound {
			return CloseNotFound, "trip not found"
		}
		s.log.Error("load trip for websocket", "error", err)
		return websocket.StatusInternalError, "internal error"
	}

	// 3. Presence: online while connected.
	h.connected(ctx, s.tripID, userID)
	defer h.disconnected(s.tripID, userID)

	participants, err := h.store.Participants(ctx, userID, s.tripID)
	if err != nil {
		s.log.Error("load participants for websocket", "error", err)
		return websocket.StatusInternalError, "internal error"
	}
	if err := s.send(ctx, readyMessage{Type: "ready", UserID: userID, Trip: trip, Participants: participants}); err != nil {
		return websocket.StatusGoingAway, ""
	}
	s.log.Debug("websocket connected")

	// 4. Read client messages in the background.
	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop(ctx) }()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	presence := time.NewTicker(PresenceInterval)
	defer presence.Stop()
	expiry := time.NewTimer(time.Until(expires))
	defer expiry.Stop()

	for {
		select {
		case <-h.shutdown:
			return websocket.StatusGoingAway, "server is shutting down; reconnect"
		case err := <-readErr:
			var c closeWith
			if errors.As(err, &c) {
				return c.code, c.reason
			}
			return websocket.StatusNormalClosure, "" // the client closed or went away
		case data, ok := <-sub.C:
			if !ok {
				return CloseResync, "events may have been missed; reconnect"
			}
			var ev ChangeEvent
			if json.Unmarshal(data, &ev) == nil {
				switch {
				case ev.Type == "removed" && ev.UserID == userID:
					s.write(ctx, data)
					return CloseForbidden, "you are no longer a participant of this trip"
				case ev.Type == "deleted":
					s.write(ctx, data)
					return CloseNotFound, "the trip was deleted"
				}
			}
			if err := s.write(ctx, data); err != nil {
				return websocket.StatusGoingAway, ""
			}
		case <-presence.C:
			h.heartbeat(ctx, s.tripID, userID)
		case <-ping.C:
			go func() {
				pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
				defer pcancel()
				if err := s.conn.Ping(pctx); err != nil {
					s.conn.CloseNow() // dead peer: unblocks the reader
				}
			}()
		case <-expiry.C:
			s.mu.Lock()
			exp := s.expires
			s.mu.Unlock()
			if wait := time.Until(exp); wait > 0 {
				expiry.Reset(wait) // the client sent a fresh token
				continue
			}
			return CloseUnauthorized, "access token expired; refresh it and reconnect"
		}
	}
}

// closeWith asks the session to close with a code.
type closeWith struct {
	code   websocket.StatusCode
	reason string
}

func (c closeWith) Error() string { return c.reason }

func (s *session) readLoop(ctx context.Context) error {
	h := s.hub
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			return closeWith{websocket.StatusUnsupportedData, "send JSON text messages"}
		}
		var m clientMessage
		if err := json.Unmarshal(data, &m); err != nil {
			metrics.WSMessagesReceived.WithLabelValues("invalid").Inc()
			s.sendError(ctx, "invalid JSON message", "invalid_message")
			continue
		}
		switch m.Type {
		case "location":
			metrics.WSMessagesReceived.WithLabelValues("location").Inc()
			if err := s.location(ctx, m.Position); err != nil {
				return err
			}
		case "auth":
			metrics.WSMessagesReceived.WithLabelValues("auth").Inc()
			userID, expires, err := h.auth.Authenticate(ctx, m.Token)
			if err != nil || userID != s.userID {
				return closeWith{CloseUnauthorized, "invalid access token"}
			}
			s.mu.Lock()
			s.expires = expires
			s.mu.Unlock()
		case "ping":
			metrics.WSMessagesReceived.WithLabelValues("ping").Inc()
			s.send(ctx, map[string]string{"type": "pong"})
		default:
			metrics.WSMessagesReceived.WithLabelValues("unknown").Inc()
			s.sendError(ctx, "unknown message type", "invalid_message")
		}
	}
}

// location stores and broadcasts one reported position. Errors that end
// the connection are returned; others are reported to the client.
func (s *session) location(ctx context.Context, pos Position) error {
	h := s.hub
	if ok, _ := h.limiter.Allow(s.tripID + ":" + s.userID); !ok {
		metrics.LocationsRejected.WithLabelValues("rate_limited").Inc()
		metrics.RateLimited.WithLabelValues("ws_location").Inc()
		s.sendError(ctx, "too many location updates; slow down", "rate_limited")
		return nil
	}
	newest, err := h.store.AddLocations(ctx, s.tripID, s.userID, []Position{pos})
	switch {
	case err == nil:
		metrics.LocationUpdates.WithLabelValues("ws").Inc()
		if newest != nil {
			h.publish(ctx, s.tripID, LocationEvent{Type: "location", UserID: s.userID, Position: *newest})
		}
		return nil
	case errors.Is(err, ErrNotFound):
		return closeWith{CloseForbidden, "you are no longer a participant of this trip"}
	case errors.Is(err, errNotActive):
		metrics.LocationsRejected.WithLabelValues("inactive").Inc()
		s.sendError(ctx, err.Error(), "trip_not_active")
		return nil
	case apperr.KindOf(err) == apperr.Invalid:
		metrics.LocationsRejected.WithLabelValues("invalid").Inc()
		s.sendError(ctx, err.Error(), "invalid_location")
		return nil
	default:
		if ctx.Err() == nil {
			s.log.Error("store location", "error", err)
		}
		s.sendError(ctx, "could not store the location; try again", "internal")
		return nil
	}
}

func (s *session) send(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(ctx, data)
}

func (s *session) sendError(ctx context.Context, msg, code string) {
	s.send(ctx, errorMessage{Type: "error", Error: msg, Code: code})
}

// write sends a message; a client that doesn't take it within the write
// timeout is disconnected.
func (s *session) write(ctx context.Context, data []byte) error {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := s.conn.Write(wctx, websocket.MessageText, data); err != nil {
		return err
	}
	metrics.WSMessagesSent.Inc()
	return nil
}

// connected marks the participant online and tells the others.
func (h *Hub) connected(ctx context.Context, tripID, userID string) {
	h.mu.Lock()
	h.conns[presenceKey{tripID, userID}]++
	h.mu.Unlock()
	h.heartbeat(ctx, tripID, userID)
}

// heartbeat refreshes the participant's presence. Repeating the event
// heals presence shown as offline by another instance.
func (h *Hub) heartbeat(ctx context.Context, tripID, userID string) {
	if err := h.store.SetPresence(ctx, tripID, userID, true); err != nil && ctx.Err() == nil {
		h.log.Warn("update presence", "trip_id", tripID, "user_id", userID, "error", err)
	}
	h.publish(ctx, tripID, PresenceEvent{Type: "presence", UserID: userID, Online: true, At: time.Now().UTC()})
}

// disconnected marks the participant offline when their last connection
// on this instance closes.
func (h *Hub) disconnected(tripID, userID string) {
	k := presenceKey{tripID, userID}
	h.mu.Lock()
	h.conns[k]--
	last := h.conns[k] <= 0
	if last {
		delete(h.conns, k)
	}
	h.mu.Unlock()
	if !last {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.SetPresence(ctx, tripID, userID, false); err != nil {
		h.log.Warn("update presence", "trip_id", tripID, "user_id", userID, "error", err)
	}
	h.publish(ctx, tripID, PresenceEvent{Type: "presence", UserID: userID, Online: false, At: time.Now().UTC()})
}

// publish sends an event to everyone connected to the trip, on all instances.
func (h *Hub) publish(ctx context.Context, tripID string, ev any) {
	data, err := json.Marshal(ev)
	if err == nil {
		err = h.broker.Publish(ctx, pubsub.TripTopic(tripID), data)
	}
	if err != nil && ctx.Err() == nil {
		h.log.Warn("publish trip event", "trip_id", tripID, "error", err)
	}
}
