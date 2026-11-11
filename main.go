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
	"github.com/weeks6/travel-docs/server"
	"github.com/weeks6/travel-docs/services/auth"
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

	priv, pub, err := auth.LoadKeyFiles(
		getenv("JWT_PRIVATE_KEY_FILE", "test_key.pem"),
		getenv("JWT_PUBLIC_KEY_FILE", "test_key.pub.pem"))
	if err != nil {
		log.Fatal(err)
	}
	authSvc, err := auth.New(pool, auth.Config{
		PrivateKeyPEM: priv,
		PublicKeyPEM:  pub,
	})
	if err != nil {
		log.Fatal(err)
	}

	s := &http.Server{
		Addr:              getenv("ADDR", ":8080"),
		Handler:           server.New(pool, authSvc),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // room for GPX uploads
		WriteTimeout:      60 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
