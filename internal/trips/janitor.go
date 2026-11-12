package trips

import (
	"context"
	"log/slog"
	"time"

	"github.com/weeks6/travel-docs/internal/logging"
	"github.com/weeks6/travel-docs/internal/metrics"
)

// Janitor applies the location retention policy and expires stale
// presence in the background.
type Janitor struct {
	Store *Store
	Log   *slog.Logger
	// Interval between retention runs. Default 10 minutes.
	Interval time.Duration
}

// Run works until ctx is canceled.
func (j *Janitor) Run(ctx context.Context) {
	interval := j.Interval
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	retention := time.NewTicker(interval)
	defer retention.Stop()
	presence := time.NewTicker(time.Minute)
	defer presence.Stop()
	j.retention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-retention.C:
			j.retention(ctx)
		case <-presence.C:
			if err := j.Store.ExpirePresence(ctx); err != nil && ctx.Err() == nil {
				j.Log.Error("expire trip presence", "error", err)
			}
		}
	}
}

func (j *Janitor) retention(ctx context.Context) {
	start := time.Now()
	n, err := j.Store.ApplyRetention(ctx)
	if err != nil {
		if ctx.Err() == nil {
			j.Log.Error("apply location retention", "error", err)
		}
		return
	}
	metrics.LocationsDeleted.Add(float64(n))
	if n > 0 {
		j.Log.Info("deleted expired trip locations", "count", n, "duration_ms", logging.Millis(time.Since(start)))
	}
}
