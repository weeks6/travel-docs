package imports

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/gpx"
	"github.com/weeks6/travel-docs/internal/logging"
	"github.com/weeks6/travel-docs/internal/metrics"
	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/routes"
	"github.com/weeks6/travel-docs/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// errShutdown is the cancellation cause for jobs interrupted by shutdown.
var errShutdown = errors.New("server shutting down")

// Config tunes the worker pool. Zero values use the defaults.
type Config struct {
	// Workers is how many imports run at the same time on this instance;
	// it bounds the memory used for parsing large files. Default 2.
	Workers int
	// PollInterval is how often idle workers look for jobs (they are also
	// woken when a job is queued). Default 2s.
	PollInterval time.Duration
	// Lease is how long a job stays claimed without a heartbeat. Default 30s.
	Lease time.Duration
	// ShutdownGrace is how long running jobs may continue after shutdown
	// starts; then they are put back into the queue. Default 10s.
	ShutdownGrace time.Duration
	// Retention is how long finished jobs are kept. Default 7 days.
	Retention time.Duration
	// RetryBaseDelay is the delay before the first retry; it doubles with
	// every attempt (up to a minute). Default 2s.
	RetryBaseDelay time.Duration
}

func (c *Config) setDefaults() {
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.Lease <= 0 {
		c.Lease = 30 * time.Second
	}
	if c.ShutdownGrace <= 0 {
		c.ShutdownGrace = 10 * time.Second
	}
	if c.Retention <= 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	if c.RetryBaseDelay <= 0 {
		c.RetryBaseDelay = 2 * time.Second
	}
}

// Runner is the pool of import workers of one app instance.
type Runner struct {
	store  *Store
	pool   *pgxpool.Pool
	routes *routes.Store
	broker pubsub.Broker
	log    *slog.Logger
	cfg    Config
	id     string // identifies this instance's leases

	wake chan struct{}

	mu      sync.Mutex
	running map[string]context.CancelCauseFunc
}

func NewRunner(pool *pgxpool.Pool, routeStore *routes.Store, broker pubsub.Broker, log *slog.Logger, cfg Config) *Runner {
	cfg.setDefaults()
	host, _ := os.Hostname()
	b := make([]byte, 4)
	rand.Read(b)
	return &Runner{
		store:   NewStore(pool),
		pool:    pool,
		routes:  routeStore,
		broker:  broker,
		log:     log.With("component", "import_worker"),
		cfg:     cfg,
		id:      fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b)),
		wake:    make(chan struct{}, 1),
		running: map[string]context.CancelCauseFunc{},
	}
}

// Run processes jobs until ctx is canceled. Then it stops taking new jobs,
// lets running jobs finish for up to ShutdownGrace, puts the rest back into
// the queue and returns.
func (r *Runner) Run(ctx context.Context) {
	// Jobs run under their own context so shutdown can give them time.
	jobCtx, stopJobs := context.WithCancelCause(context.WithoutCancel(ctx))
	defer stopJobs(nil)

	r.log.Info("import workers started", "workers", r.cfg.Workers, "instance", r.id)
	var wg sync.WaitGroup
	for range r.cfg.Workers {
		wg.Go(func() { r.worker(ctx, jobCtx) })
	}
	wg.Go(func() { r.listen(ctx) })
	wg.Go(func() { r.maintain(ctx) })

	<-ctx.Done()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(r.cfg.ShutdownGrace):
		r.log.Warn("import jobs still running after grace period; putting them back into the queue")
		stopJobs(errShutdown)
		<-done
	}
	r.log.Info("import workers stopped")
}

func (r *Runner) worker(ctx, jobCtx context.Context) {
	for ctx.Err() == nil {
		job, payload, err := r.store.claim(ctx, r.id, r.cfg.Lease)
		switch {
		case err == nil:
			r.signal() // there may be more: let another worker look
			r.process(jobCtx, job, payload)
			continue
		case errors.Is(err, ErrNotFound):
		case ctx.Err() != nil:
			return
		default:
			r.log.Error("claim import job", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

// signal wakes one idle worker of this instance.
func (r *Runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// listen handles control messages: wake-ups for new jobs and cancel
// requests for jobs running here.
func (r *Runner) listen(ctx context.Context) {
	for ctx.Err() == nil {
		sub, err := r.broker.Subscribe(ctx, ControlTopic)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error("subscribe to import job control", "error", err)
			}
		} else {
			r.handleControl(ctx, sub)
			sub.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (r *Runner) handleControl(ctx context.Context, sub *pubsub.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-sub.C:
			if !ok {
				return
			}
			var m control
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.Op {
			case "wake":
				r.signal()
			case "cancel":
				r.cancelLocal(m.ID)
			}
		}
	}
}

// cancelLocal stops the job if it runs on this instance.
func (r *Runner) cancelLocal(id string) {
	r.mu.Lock()
	cancel := r.running[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel(errCanceled)
	}
}

// maintain requeues jobs of crashed workers, samples the queue length and
// deletes old jobs.
func (r *Runner) maintain(ctx context.Context) {
	tick := time.NewTicker(max(r.cfg.Lease/2, time.Second))
	defer tick.Stop()
	var lastCleanup time.Time
	for {
		jobs, err := r.store.reclaimExpired(ctx)
		if err != nil && ctx.Err() == nil {
			r.log.Error("reclaim expired import jobs", "error", err)
		}
		for _, j := range jobs {
			r.log.Warn("import job lease expired", "job_id", j.ID, "status", j.Status, "attempt", j.Attempts)
			r.publish(ctx, j)
		}
		if len(jobs) > 0 {
			r.signal()
		}
		if n, err := r.store.queueDepth(ctx); err == nil {
			metrics.ImportJobsQueued.Set(float64(n))
		}
		if time.Since(lastCleanup) > time.Hour {
			if n, err := r.store.cleanup(ctx, r.cfg.Retention); err != nil {
				if ctx.Err() == nil {
					r.log.Error("clean up import jobs", "error", err)
				}
			} else {
				lastCleanup = time.Now()
				if n > 0 {
					r.log.Info("deleted old import jobs", "count", n)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// process runs one claimed job and records the outcome.
func (r *Runner) process(parent context.Context, job Job, payload []byte) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	r.mu.Lock()
	r.running[job.ID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, job.ID)
		r.mu.Unlock()
	}()
	metrics.ImportJobsRunning.Inc()
	defer metrics.ImportJobsRunning.Dec()

	ctx, span := tracing.Tracer().Start(ctx, "import_job.process")
	defer span.End()
	span.SetAttributes(tracing.Attr("job.id", job.ID), tracing.Attr("job.kind", job.Kind),
		attribute.Int("job.attempt", job.Attempts), attribute.Int64("job.size_bytes", job.SizeBytes))

	log := r.log.With("job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts, "owner_id", job.OwnerID)
	log = log.With(tracing.LogAttrs(ctx)...)
	log.Info("import job started", "size_bytes", job.SizeBytes, "filename", job.Filename)
	r.publish(ctx, job)
	start := time.Now()

	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		r.heartbeat(ctx, cancel, job.ID)
	}()

	result, err := r.run(ctx, log, job, payload)
	cause := context.Cause(ctx)
	cancel(nil) // stops the heartbeat
	<-hbDone

	// Record the outcome even if the job's context was canceled.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer fcancel()
	outcome := "succeeded"
	var final Job
	var ferr error
	var retryIn time.Duration
	switch {
	case err == nil:
		final = result
	case errors.Is(err, errLeaseLost) || errors.Is(cause, errLeaseLost):
		outcome = "lease_lost"
	case errors.Is(err, errCanceled) || errors.Is(cause, errCanceled):
		outcome = "canceled"
		final, ferr = r.store.markCanceled(fctx, job.ID, r.id)
	case errors.Is(cause, errShutdown):
		outcome = "released"
		final, ferr = r.store.release(fctx, job.ID, r.id)
	case permanent(err):
		outcome = "failed"
		final, ferr = r.store.fail(fctx, job.ID, r.id, err.Error())
	case job.Attempts < job.MaxAttempts:
		outcome = "retry"
		retryIn = r.retryDelay(job.Attempts)
		log.Warn("import job attempt failed; will retry", "error", err, "retry_in_ms", logging.Millis(retryIn))
		// Internal details are logged, not shown to the user.
		final, ferr = r.store.requeue(fctx, job.ID, r.id, "a temporary server error occurred; retrying", retryIn)
	default:
		outcome = "failed"
		log.Error("import job failed after all attempts", "error", err)
		final, ferr = r.store.fail(fctx, job.ID, r.id,
			fmt.Sprintf("failed after %d attempts because of a server error; try again later", job.Attempts))
	}
	if errors.Is(ferr, errLeaseLost) {
		outcome = "lease_lost"
	} else if ferr != nil {
		// The lease expires and the job is picked up again.
		log.Error("record import job outcome", "outcome", outcome, "error", ferr)
	}
	if final.ID != "" {
		r.publish(fctx, final)
	}
	if outcome == "retry" && ferr == nil {
		// Wake a worker once the retry is due.
		time.AfterFunc(retryIn, r.signal)
	}

	elapsed := time.Since(start)
	metrics.ImportJobs.WithLabelValues(outcome).Inc()
	metrics.ImportJobDuration.WithLabelValues(outcome).Observe(elapsed.Seconds())
	span.SetAttributes(tracing.Attr("job.outcome", outcome))
	attrs := []any{"outcome", outcome, "duration_ms", logging.Millis(elapsed)}
	if final.RouteID != nil {
		attrs = append(attrs, "route_id", *final.RouteID)
	}
	if err != nil && outcome != "retry" {
		attrs = append(attrs, "error", err)
		if outcome == "failed" {
			span.SetStatus(codes.Error, err.Error())
		}
	}
	log.Info("import job finished", attrs...)
}

// retryDelay grows exponentially with jitter: base, 2·base, 4·base, … ≤ 1 min.
func (r *Runner) retryDelay(attempt int) time.Duration {
	d := float64(r.cfg.RetryBaseDelay) * math.Pow(2, float64(max(attempt-1, 0)))
	d = math.Min(d, float64(time.Minute))
	return time.Duration(d * (0.8 + 0.4*mathrand.Float64()))
}

// heartbeat renews the lease until ctx ends and cancels the job when its
// owner asked for it or the lease was lost.
func (r *Runner) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, id string) {
	tick := time.NewTicker(max(r.cfg.Lease/4, 100*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cancelRequested, err := r.store.heartbeat(ctx, id, r.id, r.cfg.Lease)
		switch {
		case errors.Is(err, errLeaseLost):
			cancel(errLeaseLost)
			return
		case err != nil:
			if ctx.Err() == nil {
				r.log.Warn("renew import job lease", "job_id", id, "error", err)
			}
		case cancelRequested:
			cancel(errCanceled)
			return
		}
	}
}

// permanent reports whether retrying can't help: the file is invalid, the
// route is gone, the user lost access, or the route changed meanwhile.
func permanent(err error) bool {
	return apperr.KindOf(err) != apperr.Internal || gpx.IsValidationError(err)
}

// run parses and analyzes the file and stores the result.
func (r *Runner) run(ctx context.Context, log *slog.Logger, job Job, payload []byte) (Job, error) {
	rep := &reporter{r: r, ctx: ctx, job: job, log: log}

	rep.report(StageParsing, 5, true)
	_, span := tracing.Tracer().Start(ctx, "import_job.parse")
	f, err := routes.ParseGPX(&progressReader{
		ctx: ctx, r: bytes.NewReader(payload), total: int64(len(payload)),
		onRead: func(read, total int64) { rep.report(StageParsing, 5+int(55*read/max(total, 1)), false) },
	})
	span.End()
	if err != nil {
		return Job{}, err
	}
	payload = nil // let the file be collected while saving

	rep.report(StageAnalyzing, 60, true)
	if err := context.Cause(ctx); err != nil {
		return Job{}, err
	}
	_, span = tracing.Tracer().Start(ctx, "import_job.analyze")
	content, err := routes.BuildContent(f, job.Filename, job.Name)
	span.End()
	if err != nil {
		return Job{}, err
	}
	f = nil
	if err := context.Cause(ctx); err != nil {
		return Job{}, err
	}

	rep.report(StageSaving, 85, true)
	ctx, span = tracing.Tracer().Start(ctx, "import_job.save")
	defer span.End()
	var final Job
	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := lockForCompletion(ctx, tx, job.ID, r.id); err != nil {
			return err
		}
		details := map[string]any{"import_job_id": job.ID}
		var route routes.Route
		var err error
		switch job.Kind {
		case KindCreate:
			route, err = r.routes.CreateInTx(ctx, tx, job.OwnerID, content, job.Visibility, details)
		case KindReplaceGeometry:
			if job.RouteID == nil {
				return apperr.New(apperr.NotFound, "the route was deleted")
			}
			ifVersion := 0
			if job.IfVersion != nil {
				ifVersion = *job.IfVersion
			}
			route, err = r.routes.ReplaceGeometryInTx(ctx, tx, job.OwnerID, *job.RouteID, content, false, ifVersion, details)
		default:
			return apperr.Newf(apperr.Invalid, "unknown import job kind %q", job.Kind)
		}
		if err != nil {
			return err
		}
		final, err = completeInTx(ctx, tx, job.ID, route.ID)
		return err
	})
	if err != nil && context.Cause(ctx) != nil && !permanent(err) {
		return Job{}, context.Cause(ctx)
	}
	return final, err
}

// reporter saves and publishes progress, at most every 250 ms unless the
// stage changes.
type reporter struct {
	r        *Runner
	ctx      context.Context
	job      Job
	log      *slog.Logger
	stage    string
	progress int
	last     time.Time
}

func (p *reporter) report(stage string, pct int, force bool) {
	if !force && (stage == p.stage && (pct-p.progress < 5 || time.Since(p.last) < 250*time.Millisecond)) {
		return
	}
	if stage == p.stage && pct == p.progress {
		return
	}
	p.stage, p.progress, p.last = stage, pct, time.Now()
	j, err := p.r.store.progress(p.ctx, p.job.ID, p.r.id, stage, pct)
	if err != nil {
		if p.ctx.Err() == nil && !errors.Is(err, errLeaseLost) {
			p.log.Warn("save import progress", "error", err)
		}
		return
	}
	p.r.publish(p.ctx, j)
}

// publish sends the job's new state to its owner's event stream.
func (r *Runner) publish(ctx context.Context, j Job) {
	Publish(ctx, r.broker, r.log, j)
}

// Publish sends a job update to the owner's event stream.
func Publish(ctx context.Context, b pubsub.Broker, log *slog.Logger, j Job) {
	pubsub.Notify(ctx, b, log, pubsub.UserTopic(j.OwnerID), Event{Type: EventJob, Job: j})
}

// progressReader reports how much of the file was read and stops when ctx
// is canceled.
type progressReader struct {
	ctx    context.Context
	r      io.Reader
	read   int64
	total  int64
	onRead func(read, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	// Small reads so cancellation and progress stay responsive.
	if len(b) > 256<<10 {
		b = b[:256<<10]
	}
	n, err := p.r.Read(b)
	p.read += int64(n)
	p.onRead(p.read, p.total)
	return n, err
}
