// Command travel-docs serves the Travel Docs API. See README.md for its
// configuration (environment variables) and internal/app for how it runs.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/weeks6/travel-docs/internal/app"
	"github.com/weeks6/travel-docs/internal/config"
	"github.com/weeks6/travel-docs/internal/logging"
)

func main() {
	log, err := logging.New(os.Stderr, getenv("LOG_FORMAT", "json"), getenv("LOG_LEVEL", "info"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = app.Run(ctx, log, cfg)
		stop()
	}
	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
