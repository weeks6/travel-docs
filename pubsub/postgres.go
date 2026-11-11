package pubsub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/metrics"
)

// channel is the PostgreSQL notification channel used for all topics.
const channel = "travel_docs_events"

// maxPayload is PostgreSQL's NOTIFY payload limit (8000 bytes) minus a
// little room.
const maxPayload = 7900

// ErrTooLarge is returned for events that don't fit into a notification.
var ErrTooLarge = errors.New("pubsub: event too large")

// Postgres is a broker backed by PostgreSQL LISTEN/NOTIFY. Run must be
// running for subscribers to receive events.
type Postgres struct {
	pool  *pgxpool.Pool
	hub   *hub
	log   *slog.Logger
	ready chan struct{}
	once  sync.Once
}

func NewPostgres(pool *pgxpool.Pool, log *slog.Logger) *Postgres {
	return &Postgres{pool: pool, hub: newHub(), log: log, ready: make(chan struct{})}
}

// Ready is closed once the broker listens for the first time.
func (p *Postgres) Ready() <-chan struct{} { return p.ready }

// Publish sends an event to every instance. Events are delivered in the
// order PostgreSQL commits them.
func (p *Postgres) Publish(ctx context.Context, topic string, data []byte) error {
	payload := topic + "\n" + string(data)
	if len(payload) > maxPayload {
		return fmt.Errorf("%w: %d bytes on %s", ErrTooLarge, len(payload), topic)
	}
	if _, err := p.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload); err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	metrics.PubSubPublished.Inc()
	return nil
}

func (p *Postgres) Subscribe(topic string) *Subscription { return p.hub.subscribe(topic) }

// Run listens for events until ctx is done, reconnecting after errors.
// Subscribers are dropped on reconnect since events may have been missed,
// and when Run returns.
func (p *Postgres) Run(ctx context.Context) {
	defer p.hub.dropAll(true)
	const initialBackoff = 500 * time.Millisecond
	backoff := initialBackoff
	listened := false // whether subscribers may have been receiving events
	for ctx.Err() == nil {
		if listened {
			p.hub.dropAll(false)
		}
		ok, err := p.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		if ok {
			listened = true
			backoff = initialBackoff
		}
		p.log.Error("pubsub listener stopped; reconnecting", "error", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 15*time.Second)
	}
}

// listen reports whether it got to listening before failing.
func (p *Postgres) listen(ctx context.Context) (bool, error) {
	pc, err := p.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	// The connection keeps LISTEN state, so take it out of the pool.
	conn := pc.Hijack()
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return false, err
	}
	p.once.Do(func() { close(p.ready) })
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		topic, data, ok := bytes.Cut([]byte(n.Payload), []byte("\n"))
		if !ok {
			continue
		}
		p.hub.deliver(string(topic), data)
	}
}
