// Package server assembles the HTTP application on top of Echo.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/audit"
	"github.com/weeks6/travel-docs/services/auth"
	"github.com/weeks6/travel-docs/services/collections"
	"github.com/weeks6/travel-docs/services/routes"
	"github.com/weeks6/travel-docs/web"
)

// Content-Security-Policy: scripts and styles only from this server and the
// Leaflet CDN, map tiles from OpenStreetMap, API calls to this server only.
const csp = "default-src 'self'; " +
	"script-src 'self' https://unpkg.com; " +
	"style-src 'self' https://unpkg.com; " +
	"img-src 'self' data: https://tile.openstreetmap.org https://unpkg.com; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// New returns the application's HTTP handler.
func New(pool *pgxpool.Pool, authSvc *auth.Service) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errorHandler
	e.JSONSerializer = strictJSON{}

	e.Use(
		middleware.RequestID(),
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
					"latency", v.Latency.Round(time.Millisecond), "request_id", v.RequestID}
				if v.Error != nil && v.Status >= 500 {
					slog.Error("request", append(attrs, "error", v.Error)...)
				} else {
					slog.Info("request", attrs...)
				}
				return nil
			},
		}),
		middleware.Recover(),
		middleware.SecureWithConfig(middleware.SecureConfig{
			ContentTypeNosniff: "nosniff",
			XFrameOptions:      "DENY",
			// Other sites get only our origin, never paths. OpenStreetMap tiles
			// require a Referer and are blocked without one.
			ReferrerPolicy:        "strict-origin-when-cross-origin",
			ContentSecurityPolicy: csp,
		}),
		authSvc.Middleware,
	)

	authSvc.RegisterHandlers(e)
	(&routes.Handler{Store: routes.NewStore(pool)}).Register(e)
	(&collections.Handler{Store: collections.NewStore(pool)}).Register(e)
	(&audit.Handler{Store: &audit.Store{Pool: pool}, CurrentUser: auth.RequireUser}).Register(e)
	e.GET("/healthz", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "database unavailable").Wrap(err)
		}
		return c.String(http.StatusOK, "ok")
	})
	web.Register(e)
	return e
}

var statusByKind = map[apperr.Kind]int{
	apperr.Invalid:            http.StatusBadRequest,
	apperr.Unauthorized:       http.StatusUnauthorized,
	apperr.Forbidden:          http.StatusForbidden,
	apperr.NotFound:           http.StatusNotFound,
	apperr.Conflict:           http.StatusConflict,
	apperr.PreconditionFailed: http.StatusPreconditionFailed,
	apperr.Unprocessable:      http.StatusUnprocessableEntity,
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
		// Echo's own errors: routing (404/405), body limit (413), binding (400/415).
		status = code
		if status < 500 {
			msg = http.StatusText(code)
			var he *echo.HTTPError
			if errors.As(err, &he) && he.Message != "" {
				msg = he.Message
			}
		}
	}
	if status == http.StatusUnauthorized {
		c.Response().Header().Set("WWW-Authenticate", `Bearer realm="travel-docs"`)
	}
	if c.Request().Method == http.MethodHead {
		err = c.NoContent(status)
	} else {
		err = c.JSON(status, map[string]string{"error": msg})
	}
	if err != nil {
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
