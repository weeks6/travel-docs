// Package app assembles the API from its parts. New builds the HTTP
// handler and background work on top of given resources (used by tests and
// by Run); Run also opens those resources from the configuration and serves
// until shut down.
package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/config"
	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/routes"
	"github.com/weeks6/travel-docs/internal/routes/imports"
	"github.com/weeks6/travel-docs/internal/server"
	"github.com/weeks6/travel-docs/internal/trips"
)

// Deps are the external resources the application runs on.
type Deps struct {
	Pool   *pgxpool.Pool
	Auth   *auth.Service
	Broker pubsub.Broker
	Log    *slog.Logger
}

// App is the API's HTTP handler plus the background work it relies on.
type App struct {
	// Handler serves the API. Call Handler.CloseStreams when shutdown
	// starts so streaming clients reconnect elsewhere.
	Handler *server.Server

	deps Deps
	cfg  config.Config
}

// New builds the application. Only the fields of cfg that concern the API
// itself are used (not addresses or connection settings).
func New(d Deps, cfg config.Config) (*App, error) {
	h, err := server.New(server.Config{
		Pool: d.Pool, Auth: d.Auth, Broker: d.Broker, Log: d.Log,
		CORSOrigins: cfg.CORSOrigins, RateLimits: cfg.RateLimits, UploadSlots: cfg.UploadSlots,
	})
	if err != nil {
		return nil, err
	}
	return &App{Handler: h, deps: d, cfg: cfg}, nil
}

// RunWorkers processes import jobs until ctx is canceled, then lets running
// jobs finish (or hands them back) and returns.
func (a *App) RunWorkers(ctx context.Context) {
	imports.NewRunner(a.deps.Pool, routes.NewStore(a.deps.Pool), a.deps.Broker, a.deps.Log, a.cfg.Jobs).Run(ctx)
}

// RunMaintenance applies the trip location retention policy and expires
// stale presence until ctx is canceled.
func (a *App) RunMaintenance(ctx context.Context) {
	j := &trips.Janitor{Store: trips.NewStore(a.deps.Pool), Log: a.deps.Log.With("component", "trip_janitor")}
	j.Run(ctx)
}
