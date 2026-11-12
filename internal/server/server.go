// Package server assembles the HTTP API on top of Echo.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/audit"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/collections"
	"github.com/weeks6/travel-docs/internal/events"
	"github.com/weeks6/travel-docs/internal/logging"
	"github.com/weeks6/travel-docs/internal/metrics"
	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/ratelimit"
	"github.com/weeks6/travel-docs/internal/routes"
	"github.com/weeks6/travel-docs/internal/routes/imports"
	"github.com/weeks6/travel-docs/internal/tracing"
	"github.com/weeks6/travel-docs/internal/trips"
)

// The API serves JSON only; the web UI is a separate app.
const csp = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'"

// RateLimits configures request limits. Zero rates disable a limit.
type RateLimits struct {
	// PerSecond and Burst limit all API requests per user (or per IP
	// address for anonymous requests).
	PerSecond float64
	Burst     int
	// AuthPerMinute and AuthBurst limit login, registration and token
	// refresh per IP address (password guessing).
	AuthPerMinute float64
	AuthBurst     int
	// LocationsPerSecond and LocationsBurst limit live location messages
	// per trip participant.
	LocationsPerSecond float64
	LocationsBurst     int
}

// Config holds the server's dependencies and settings.
type Config struct {
	Pool   *pgxpool.Pool
	Auth   *auth.Service
	Broker pubsub.Broker
	Log    *slog.Logger
	// CORSOrigins are the browser origins allowed to call the API, e.g.
	// "http://localhost:3000". "*" allows any origin.
	CORSOrigins []string
	RateLimits  RateLimits
	// UploadSlots limits GPX uploads read into memory at the same time.
	UploadSlots int
}

// Server is the HTTP handler plus the long-lived connections it owns.
type Server struct {
	*echo.Echo
	hub           *trips.Hub
	streams       chan struct{}
	closeStreamsO sync.Once
}

// CloseStreams ends event streams and WebSocket connections (clients
// reconnect to another instance) and waits until the WebSockets closed or
// ctx ends. Call it when shutdown starts, e.g. from
// http.Server.RegisterOnShutdown.
func (s *Server) CloseStreams(ctx context.Context) error {
	s.closeStreamsO.Do(func() { close(s.streams) })
	return s.hub.Shutdown(ctx)
}

// New returns the application's HTTP handler.
func New(cfg Config) (*Server, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Broker == nil {
		cfg.Broker = pubsub.NewLocal()
	}
	cors, err := corsMiddleware(cfg.CORSOrigins)
	if err != nil {
		return nil, err
	}
	e := echo.New()
	e.HTTPErrorHandler = errorHandler
	e.JSONSerializer = strictJSON{}
	log := cfg.Log

	e.Use(
		middleware.RequestID(),
		cors,
		tracing.Middleware,
		middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
			Skipper:      func(c *echo.Context) bool { return c.Path() == "/healthz" },
			HandleError:  true, // log the final status of errors returned by handlers
			LogMethod:    true,
			LogURI:       true,
			LogStatus:    true,
			LogLatency:   true,
			LogRequestID: true,
			LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
				attrs := []any{"method", v.Method, "uri", v.URI, "status", v.Status,
					"latency_ms", logging.Millis(v.Latency), "request_id", v.RequestID}
				ctx := c.Request().Context()
				if id := auth.UserID(ctx); id != "" {
					attrs = append(attrs, "user_id", id)
				}
				attrs = append(attrs, tracing.LogAttrs(ctx)...)
				if v.Error != nil && v.Status >= 500 {
					log.Error("request", append(attrs, "error", v.Error)...)
				} else {
					log.Info("request", attrs...)
				}
				return nil
			},
		}),
		metrics.Middleware,
		middleware.Recover(),
		middleware.SecureWithConfig(middleware.SecureConfig{
			ContentTypeNosniff:    "nosniff",
			XFrameOptions:         "DENY",
			ReferrerPolicy:        "no-referrer",
			ContentSecurityPolicy: csp,
		}),
		cfg.Auth.Middleware,
		authRateLimit(ratelimit.PerMinute(cfg.RateLimits.AuthPerMinute, cfg.RateLimits.AuthBurst)),
		apiRateLimit(ratelimit.New(cfg.RateLimits.PerSecond, cfg.RateLimits.Burst)),
	)

	s := &Server{Echo: e, streams: make(chan struct{})}
	routeStore := routes.NewStore(cfg.Pool)
	tripStore := trips.NewStore(cfg.Pool)
	s.hub = trips.NewHub(tripStore, cfg.Auth, cfg.Broker, log,
		ratelimit.New(cfg.RateLimits.LocationsPerSecond, cfg.RateLimits.LocationsBurst),
		originPatterns(cfg.CORSOrigins))

	cfg.Auth.RegisterHandlers(e)
	(&routes.Handler{Store: routeStore}).Register(e)
	(&collections.Handler{Store: collections.NewStore(cfg.Pool)}).Register(e)
	(&audit.Handler{Store: &audit.Store{Pool: cfg.Pool}, CurrentUser: auth.RequireUser}).Register(e)
	imports.NewHandler(imports.NewStore(cfg.Pool), routeStore, cfg.Broker, log, cfg.UploadSlots).Register(e)
	(&trips.Handler{Store: tripStore, Hub: s.hub, Broker: cfg.Broker, Log: log}).Register(e)
	(&events.Handler{Broker: cfg.Broker, Shutdown: s.streams}).Register(e)

	e.GET("/healthz", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if err := cfg.Pool.Ping(ctx); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable").Wrap(err)
		}
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"service": "travel-docs", "status": "ok"})
	})
	return s, nil
}

// corsMiddleware lets the given browser origins call the API. Without
// origins, cross-origin browser requests are not allowed.
func corsMiddleware(origins []string) (echo.MiddlewareFunc, error) {
	if len(origins) == 0 {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }, nil
	}
	mw, err := middleware.CORSConfig{
		AllowOrigins: origins,
		AllowMethods: []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete},
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType, "If-Match",
			"Idempotency-Key", "traceparent", "tracestate"},
		ExposeHeaders: []string{"ETag", echo.HeaderLocation, "Retry-After", "Idempotent-Replayed",
			echo.HeaderXRequestID},
		MaxAge: 600,
	}.ToMiddleware()
	if err != nil {
		return nil, fmt.Errorf("CORS: %w", err)
	}
	return mw, nil
}

// clientKey identifies the caller for rate limiting: the user when logged
// in, otherwise the IP address.
func clientKey(c *echo.Context) string {
	if id := auth.UserID(c.Request().Context()); id != "" {
		return "u:" + id
	}
	return "ip:" + c.RealIP()
}

// apiRateLimit limits all requests per client, except WebSocket upgrades:
// those authenticate inside the connection (so they would all count as one
// anonymous client per IP) and have their own message limit.
func apiRateLimit(l *ratelimit.Limiter) echo.MiddlewareFunc {
	mw := ratelimit.Middleware(l, "api", clientKey)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		withLimit := mw(next)
		return func(c *echo.Context) error {
			if c.Path() == "/trips/:id/ws" {
				return next(c)
			}
			return withLimit(c)
		}
	}
}

// authRateLimit limits the endpoints that check passwords or tokens, per IP.
func authRateLimit(l *ratelimit.Limiter) echo.MiddlewareFunc {
	limited := map[string]bool{"/auth/login": true, "/auth/register": true, "/auth/refresh": true}
	mw := ratelimit.Middleware(l, "auth", func(c *echo.Context) string { return "ip:" + c.RealIP() })
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		withLimit := mw(next)
		return func(c *echo.Context) error {
			if c.Request().Method == http.MethodPost && limited[c.Path()] {
				return withLimit(c)
			}
			return next(c)
		}
	}
}

// originPatterns turns CORS origins ("http://localhost:3000") into the
// host patterns the WebSocket origin check uses ("localhost:3000").
func originPatterns(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		if o == "*" {
			out = append(out, "*")
			continue
		}
		if i := len("https://"); len(o) > i && o[:i] == "https://" {
			o = o[i:]
		} else if i := len("http://"); len(o) > i && o[:i] == "http://" {
			o = o[i:]
		}
		out = append(out, o)
	}
	return out
}

var statusByKind = map[apperr.Kind]int{
	apperr.Invalid:            http.StatusBadRequest,
	apperr.Unauthorized:       http.StatusUnauthorized,
	apperr.Forbidden:          http.StatusForbidden,
	apperr.NotFound:           http.StatusNotFound,
	apperr.Conflict:           http.StatusConflict,
	apperr.PreconditionFailed: http.StatusPreconditionFailed,
	apperr.Unprocessable:      http.StatusUnprocessableEntity,
	apperr.TooManyRequests:    http.StatusTooManyRequests,
}

// errorHandler renders every error as {"error": "..."}. Application errors
// map to their status; anything unexpected becomes a 500 without details.
func errorHandler(c *echo.Context, err error) {
	if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
		return
	}
	status, msg := http.StatusInternalServerError, "internal server error"
	if s, ok := statusByKind[apperr.KindOf(err)]; ok {
		status, msg = s, err.Error()
	} else if code := echo.StatusCode(err); code != 0 {
		// Echo's own errors: routing (404/405), body limit (413), binding
		// (400/415), and limits (429, 503).
		status = code
		if status < 500 || status == http.StatusServiceUnavailable {
			msg = http.StatusText(code)
			var he *echo.HTTPError
			if errors.As(err, &he) && he.Message != "" {
				msg = he.Message
			}
		}
	} else if errors.Is(err, context.Canceled) && c.Request().Context().Err() != nil {
		// The client went away; nobody reads the response.
		status, msg = 499, "client closed request"
	}
	if status == http.StatusUnauthorized {
		c.Response().Header().Set("WWW-Authenticate", `Bearer realm="travel-docs"`)
	}
	if c.Request().Method == http.MethodHead {
		err = c.NoContent(status)
	} else {
		err = c.JSON(status, map[string]string{"error": msg})
	}
	if err != nil && c.Request().Context().Err() == nil {
		slog.Error("write error response", "error", err)
	}
}

// strictJSON is Echo's JSON serializer with unknown fields rejected, so typos
// and unexpected fields (e.g. "owner_id") fail loudly instead of being ignored.
type strictJSON struct{}

func (strictJSON) Serialize(c *echo.Context, i any, indent string) error {
	enc := json.NewEncoder(c.Response())
	if indent != "" {
		enc.SetIndent("", indent)
	}
	return enc.Encode(i)
}

func (strictJSON) Deserialize(c *echo.Context, i any) error {
	dec := json.NewDecoder(c.Request().Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(i); err != nil {
		switch {
		case echo.StatusCode(err) != 0: // e.g. body over the BodyLimit (413)
			return err
		case errors.Is(err, io.EOF):
			return echo.NewHTTPError(http.StatusBadRequest, "request body must be JSON")
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON body: "+err.Error()).Wrap(err)
		}
	}
	if dec.More() {
		return echo.NewHTTPError(http.StatusBadRequest, "request body must contain a single JSON object")
	}
	return nil
}
