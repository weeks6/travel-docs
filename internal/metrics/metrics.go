// Package metrics defines the application's Prometheus metrics and serves
// them at /metrics.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "travel_docs"

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "http_requests_total",
		Help: "HTTP requests by method, route pattern and status code.",
	}, []string{"method", "route", "code"})
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "http_request_duration_seconds",
		Help:    "HTTP request latency by method and route pattern (long-lived streams excluded).",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"method", "route"})
	httpInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "http_requests_in_flight",
		Help: "HTTP requests currently being served, including open streams.",
	})

	// RateLimited counts requests and messages rejected by rate limits.
	RateLimited = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "rate_limited_total",
		Help: "Requests or messages rejected by a rate limit.",
	}, []string{"scope"})

	// ImportJobs counts finished import job attempts by outcome:
	// succeeded, failed, canceled, retry (will be retried), released (shutdown).
	ImportJobs = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "import_jobs_total",
		Help: "Import job attempts by outcome.",
	}, []string{"outcome"})
	ImportJobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Name: "import_job_duration_seconds",
		Help:    "Time spent processing one import job attempt.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"outcome"})
	ImportJobsRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "import_jobs_running",
		Help: "Import jobs being processed by this instance.",
	})
	ImportJobsQueued = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "import_jobs_queued",
		Help: "Import jobs waiting in the queue (all instances, sampled).",
	})
	ImportUploadBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace, Name: "import_upload_bytes",
		Help:    "Size of uploaded GPX files queued for import.",
		Buckets: prometheus.ExponentialBuckets(16<<10, 4, 8), // 16 KiB … 256 MiB
	})

	// WSConnections is the number of open trip WebSocket connections.
	WSConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Name: "ws_connections",
		Help: "Open trip WebSocket connections on this instance.",
	})
	WSMessagesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "ws_messages_received_total",
		Help: "WebSocket messages received from clients by type.",
	}, []string{"type"})
	WSMessagesSent = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: "ws_messages_sent_total",
		Help: "WebSocket messages sent to clients.",
	})
	LocationUpdates = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "trip_location_updates_total",
		Help: "Stored location points by source (ws or http).",
	}, []string{"source"})
	LocationsRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "trip_location_rejected_total",
		Help: "Rejected location updates by reason.",
	}, []string{"reason"})
	LocationsDeleted = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: "trip_locations_deleted_total",
		Help: "Location points deleted by the retention policy.",
	})

	PubSubPublished = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: "pubsub_published_total",
		Help: "Events published.",
	})
	PubSubDelivered = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: "pubsub_delivered_total",
		Help: "Events delivered to local subscribers.",
	})
	PubSubDroppedSubscribers = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Name: "pubsub_dropped_subscribers_total",
		Help: "Subscribers dropped because they could not keep up.",
	})
)

// streaming marks requests (SSE, WebSocket) whose duration is not a latency.
const streamingKey = "metrics.streaming"

// MarkStreaming excludes the current request from the latency histogram.
func MarkStreaming(c *echo.Context) { c.Set(streamingKey, true) }

// Middleware records request counts and latencies by route pattern, so
// IDs in paths don't create new time series.
func Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		start := time.Now()
		httpInFlight.Inc()
		defer httpInFlight.Dec()

		err := next(c)

		r, _ := echo.UnwrapResponse(c.Response())
		if err != nil && r != nil && !r.Committed {
			// Write the error response now to learn its status. The error is
			// still returned; the error handler skips committed responses.
			c.Echo().HTTPErrorHandler(c, err)
		}
		route := c.Path()
		if route == "" {
			route = "unmatched"
		}
		status := http.StatusOK
		if r != nil && r.Committed {
			status = r.Status
		}
		method := c.Request().Method
		httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		if streaming, _ := c.Get(streamingKey).(bool); !streaming {
			httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		}
		return err
	}
}

// Handler serves the metrics in the Prometheus text format.
func Handler() http.Handler { return promhttp.Handler() }
