package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weeks6/travel-docs/db"
	"github.com/weeks6/travel-docs/jobs"
	"github.com/weeks6/travel-docs/logging"
	"github.com/weeks6/travel-docs/metrics"
	"github.com/weeks6/travel-docs/pubsub"
	"github.com/weeks6/travel-docs/server"
	"github.com/weeks6/travel-docs/services/auth"
	"github.com/weeks6/travel-docs/services/routes"
	"github.com/weeks6/travel-docs/services/trips"
	"github.com/weeks6/travel-docs/tracing"
)

const defaultDatabaseURL = "postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable"

func main() {
	log, err := logging.New(os.Stderr, getenv("LOG_FORMAT", "json"), getenv("LOG_LEVEL", "info"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	pool, err := db.Connect(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	priv, pub, err := auth.LoadKeyFiles(cfg.privateKeyFile, cfg.publicKeyFile)
	if err != nil {
		return err
	}
	authSvc, err := auth.New(pool, auth.Config{PrivateKeyPEM: priv, PublicKeyPEM: pub})
	if err != nil {
		return err
	}

	// Background work stops after the HTTP server has drained, since open
	// streams depend on the broker until then.
	bgCtx, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBackground()
	var background sync.WaitGroup

	var broker pubsub.Broker
	if cfg.pubsub == "local" {
		broker = pubsub.NewLocal()
	} else {
		pg := pubsub.NewPostgres(pool, log.With("component", "pubsub"))
		background.Go(func() { pg.Run(bgCtx) })
		select {
		case <-pg.Ready():
		case <-time.After(10 * time.Second):
			return errors.New("pubsub: could not LISTEN within 10s")
		}
		broker = pg
	}

	srv, err := server.New(server.Config{
		Pool: pool, Auth: authSvc, Broker: broker, Log: log,
		CORSOrigins: cfg.corsOrigins, RateLimits: cfg.rateLimits, UploadSlots: cfg.uploadSlots,
	})
	if err != nil {
		return err
	}

	// Import workers start stopping as soon as the signal arrives: they
	// finish or hand back their jobs while HTTP drains.
	runner := jobs.NewRunner(pool, routes.NewStore(pool), broker, log, cfg.jobs)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); runner.Run(ctx) }()

	janitor := &trips.Janitor{Store: trips.NewStore(pool), Log: log.With("component", "trip_janitor")}
	background.Go(func() { janitor.Run(bgCtx) })

	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv,
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
		cctx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := srv.CloseStreams(cctx); err != nil {
			log.Warn("close websockets", "error", err)
		}
	})

	var metricsServer *http.Server
	if cfg.metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		metricsServer = &http.Server{Addr: cfg.metricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Info("metrics listening", "addr", cfg.metricsAddr)
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics server", "error", err)
			}
		}()
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.addr, "import_workers", cfg.jobs.Workers, "pubsub", cfg.pubsub,
			"cors_origins", strings.Join(cfg.corsOrigins, ","))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			stop()
			<-workersDone
			return err
		}
	case <-ctx.Done():
	}

	// Graceful shutdown: stop accepting connections, end streams, let
	// in-flight requests and import jobs finish, then stop the rest.
	log.Info("shutting down", "timeout", cfg.shutdownTimeout.String())
	start := time.Now()
	sctx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(sctx); err != nil {
		log.Warn("http shutdown incomplete", "error", err)
	}
	if err := srv.CloseStreams(sctx); err != nil {
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

type config struct {
	databaseURL     string
	addr            string
	metricsAddr     string
	privateKeyFile  string
	publicKeyFile   string
	corsOrigins     []string
	pubsub          string
	uploadSlots     int
	shutdownTimeout time.Duration
	jobs            jobs.Config
	rateLimits      server.RateLimits
}

func loadConfig() (config, error) {
	c := config{
		databaseURL:    getenv("DATABASE_URL", defaultDatabaseURL),
		addr:           getenv("ADDR", ":8080"),
		metricsAddr:    getenv("METRICS_ADDR", ":9090"),
		privateKeyFile: getenv("JWT_PRIVATE_KEY_FILE", "test_key.pem"),
		publicKeyFile:  getenv("JWT_PUBLIC_KEY_FILE", "test_key.pub.pem"),
		pubsub:         getenv("PUBSUB", "postgres"),
	}
	if c.metricsAddr == "off" {
		c.metricsAddr = ""
	}
	if c.pubsub != "postgres" && c.pubsub != "local" {
		return c, fmt.Errorf("PUBSUB must be postgres or local, got %q", c.pubsub)
	}
	for _, o := range strings.Split(getenv("CORS_ORIGINS", "http://localhost:3000"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.corsOrigins = append(c.corsOrigins, o)
		}
	}

	p := envParser{}
	c.uploadSlots = p.int("UPLOAD_SLOTS", jobs.DefaultUploadSlots)
	c.shutdownTimeout = p.duration("SHUTDOWN_TIMEOUT", 25*time.Second)
	c.jobs = jobs.Config{
		Workers:       p.int("IMPORT_WORKERS", 2),
		ShutdownGrace: p.duration("IMPORT_SHUTDOWN_GRACE", 15*time.Second),
		Retention:     p.duration("IMPORT_JOB_RETENTION", 7*24*time.Hour),
	}
	c.rateLimits = server.RateLimits{
		PerSecond:          p.float("RATE_LIMIT_RPS", 20),
		Burst:              p.int("RATE_LIMIT_BURST", 40),
		AuthPerMinute:      p.float("RATE_LIMIT_AUTH_PER_MINUTE", 20),
		AuthBurst:          p.int("RATE_LIMIT_AUTH_BURST", 10),
		LocationsPerSecond: p.float("RATE_LIMIT_LOCATIONS_RPS", 2),
		LocationsBurst:     p.int("RATE_LIMIT_LOCATIONS_BURST", 10),
	}
	return c, p.err
}

// envParser reads typed settings and remembers the first error.
type envParser struct{ err error }

func (p *envParser) fail(key, v, want string) {
	if p.err == nil {
		p.err = fmt.Errorf("%s=%q must be %s", key, v, want)
	}
}

func (p *envParser) int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		p.fail(key, v, "a non-negative integer")
	}
	return n
}

func (p *envParser) float(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < 0 {
		p.fail(key, v, "a non-negative number (0 disables the limit)")
	}
	return n
}

func (p *envParser) duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(key, v, "a positive duration such as 30s or 2h")
	}
	return d
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
