package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/config"
	"github.com/weeks6/travel-docs/internal/db"
	"github.com/weeks6/travel-docs/internal/logging"
	"github.com/weeks6/travel-docs/internal/metrics"
	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/tracing"
)

// Run opens the database and broker, serves the API and runs background
// work until ctx is canceled, then shuts down gracefully:
//
//  1. stop accepting connections and end event streams and WebSockets, so
//     clients reconnect to another instance;
//  2. let in-flight requests finish while import workers finish or hand
//     back their jobs (they start stopping as soon as ctx ends);
//  3. stop the remaining background work, then close the broker, database
//     and tracing.
func Run(ctx context.Context, log *slog.Logger, cfg config.Config) error {
	// Canceling ctx (a signal) starts the shutdown; stopServing ends it
	// early when the server fails.
	ctx, stopServing := context.WithCancel(ctx)
	defer stopServing()

	shutdownTracing, err := tracing.Setup(ctx, "travel-docs")
	if err != nil {
		return err
	}
	defer func() {
		tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(tctx); err != nil {
			log.Warn("flush traces", "error", err)
		}
	}()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	priv, pub, err := auth.LoadKeyFiles(cfg.PrivateKeyFile, cfg.PublicKeyFile)
	if err != nil {
		return err
	}
	authSvc, err := auth.New(pool, auth.Config{PrivateKeyPEM: priv, PublicKeyPEM: pub})
	if err != nil {
		return err
	}

	broker, closeBroker, err := openBroker(cfg, log)
	if err != nil {
		return err
	}
	// Runs on return, after the HTTP server, streams and workers below
	// have stopped using it.
	defer closeBroker()

	a, err := New(Deps{Pool: pool, Auth: authSvc, Broker: broker, Log: log}, cfg)
	if err != nil {
		return err
	}

	// Import workers start stopping as soon as ctx ends: they finish or
	// hand back their jobs while HTTP drains.
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); a.RunWorkers(ctx) }()

	// Other background work stops after the HTTP server has drained.
	bgCtx, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBackground()
	var background sync.WaitGroup
	background.Go(func() { a.RunMaintenance(bgCtx) })

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Large GPX uploads over slow connections. Event streams and
		// WebSockets lift these deadlines for their connection.
		ReadTimeout:    5 * time.Minute,
		WriteTimeout:   6 * time.Minute,
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 1 << 20,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	// Shutdown first closes the listeners, then runs this: open streams and
	// WebSockets end so that clients reconnect to another instance.
	httpServer.RegisterOnShutdown(func() {
		cctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := a.Handler.CloseStreams(cctx); err != nil {
			log.Warn("close websockets", "error", err)
		}
	})

	var metricsServer *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		metricsServer = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Info("metrics listening", "addr", cfg.MetricsAddr)
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics server", "error", err)
			}
		}()
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "import_workers", cfg.Jobs.Workers, "pubsub", cfg.PubSub,
			"cors_origins", strings.Join(cfg.CORSOrigins, ","))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			stopServing()
			<-workersDone
			return err
		}
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	start := time.Now()
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(sctx); err != nil {
		log.Warn("http shutdown incomplete", "error", err)
	}
	if err := a.Handler.CloseStreams(sctx); err != nil {
		log.Warn("websockets still open at shutdown", "error", err)
	}
	if metricsServer != nil {
		metricsServer.Shutdown(sctx)
	}
	<-workersDone
	stopBackground()
	background.Wait()
	log.Info("shutdown complete", "duration_ms", logging.Millis(time.Since(start)))
	return nil
}

// openBroker connects to the configured event broker.
func openBroker(cfg config.Config, log *slog.Logger) (pubsub.Broker, func(), error) {
	if cfg.PubSub == "local" {
		local := pubsub.NewLocal()
		return local, local.Close, nil
	}
	nb, err := pubsub.ConnectNATS(cfg.NATSURL, log.With("component", "pubsub"))
	if err != nil {
		return nil, nil, err
	}
	return nb, nb.Close, nil
}
