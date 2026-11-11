package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/weeks6/travel-docs/db"
	"github.com/weeks6/travel-docs/services/auth"
	"github.com/weeks6/travel-docs/services/routes"
	"github.com/weeks6/travel-docs/web"
)

const defaultDatabaseURL = "postgres://travel:travel@localhost:5432/travel_docs?sslmode=disable"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, getenv("DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	auth.InitJWT()

	mux := http.NewServeMux()
	auth.RegisterHandlers(mux)
	(&routes.Handler{Store: routes.NewPostgresStore(pool)}).Register(mux)
	web.Register(mux)

	s := &http.Server{
		Addr:           getenv("ADDR", ":8080"),
		Handler:        mux,
		ReadTimeout:    30 * time.Second, // room for GPX uploads
		WriteTimeout:   30 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", s.Addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
