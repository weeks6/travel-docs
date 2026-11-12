// Package logging configures structured logging with log/slog.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// New returns a logger writing to w. format is "json" or "text"; level is
// "debug", "info", "warn" or "error".
func New(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error: %w", err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(format) {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("LOG_FORMAT must be json or text, got %q", format)
	}
}

// Millis turns a duration into milliseconds for log attributes, so JSON
// logs get a readable, queryable number (e.g. latency_ms=12.5).
func Millis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
