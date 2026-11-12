// Package ratelimit limits how often a key (user or IP address)
// may do something, using token buckets.
package ratelimit

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/internal/metrics"
	"golang.org/x/time/rate"
)

// idleTTL is how long an unused key's bucket is kept.
const idleTTL = 10 * time.Minute

// Limiter keeps one token bucket per key. A nil Limiter allows everything.
type Limiter struct {
	limit rate.Limit
	burst int

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
	now       func() time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// New allows perSecond events per second on average with bursts of up to
// burst events. It returns nil (no limit) when perSecond <= 0.
func New(perSecond float64, burst int) *Limiter {
	if perSecond <= 0 {
		return nil
	}
	return &Limiter{
		limit:   rate.Limit(perSecond),
		burst:   max(burst, 1),
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// PerMinute is New with a per-minute rate.
func PerMinute(n float64, burst int) *Limiter { return New(n/60, burst) }

// Allow reports whether one event for key may happen now. When it may not,
// retryAfter is how long until it would be allowed.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	if now.Sub(l.lastSweep) > idleTTL {
		l.sweep(now)
	}
	l.mu.Unlock()

	r := b.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Duration(math.MaxInt64)
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// sweep removes buckets not used recently. Called with mu held.
func (l *Limiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.seen) > idleTTL {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}

// Middleware rejects requests over the limit with 429 Too Many Requests and
// a Retry-After header. key returns the bucket key for a request; scope
// labels the metric.
func Middleware(l *Limiter, scope string, key func(c *echo.Context) string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		if l == nil {
			return next
		}
		return func(c *echo.Context) error {
			if ok, wait := l.Allow(key(c)); !ok {
				metrics.RateLimited.WithLabelValues(scope).Inc()
				c.Response().Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(wait)))
				return echo.NewHTTPError(http.StatusTooManyRequests, "too many requests; slow down and try again later")
			}
			return next(c)
		}
	}
}

// RetryAfterSeconds rounds a wait up to whole seconds (at least 1).
func RetryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	if s > 3600 {
		return 3600
	}
	return s
}
