package pubsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/weeks6/travel-docs/metrics"
)

// subjectPrefix namespaces this application's subjects on a shared server.
const subjectPrefix = "travel_docs."

// subscribeTimeout bounds the round trip that confirms a subscription when
// the caller's context has no deadline.
const subscribeTimeout = 5 * time.Second

// ErrTooLarge is returned for events larger than the server's max payload.
var ErrTooLarge = errors.New("pubsub: event too large")

// NATS is a broker backed by core NATS (at-most-once, fan-out to every
// subscribed instance). The client reconnects on its own; since events may
// be missed meanwhile, all subscriptions are dropped when the connection is
// lost and again when it is re-established.
type NATS struct {
	nc   *nats.Conn
	log  *slog.Logger
	subs subscriptionSet
}

// ConnectNATS connects to the NATS server(s) at url (comma-separated for a
// cluster). It fails if no server is reachable; later outages are retried
// forever. Extra options are applied after the defaults.
func ConnectNATS(url string, log *slog.Logger, opts ...nats.Option) (*NATS, error) {
	b := &NATS{log: log}
	defaults := []nats.Option{
		nats.Name("travel-docs"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500 * time.Millisecond),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("pubsub disconnected; reconnecting", "error", err)
			}
			b.subs.dropAll(false)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Info("pubsub reconnected", "server", nc.ConnectedUrlRedacted())
			// Subscriptions made during the outage have missed events too.
			b.subs.dropAll(false)
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			subject := ""
			if sub != nil {
				subject = sub.Subject
			}
			log.Warn("pubsub error", "subject", subject, "error", err)
		}),
	}
	nc, err := nats.Connect(url, append(defaults, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS at %s: %w", url, err)
	}
	b.nc = nc
	return b, nil
}

// Publish sends an event to every subscribed instance. Events from one
// publisher arrive in order. While reconnecting, events are buffered by the
// client.
func (b *NATS) Publish(_ context.Context, topic string, data []byte) error {
	if max := b.nc.MaxPayload(); max > 0 && int64(len(data)) > max {
		return fmt.Errorf("%w: %d bytes on %s", ErrTooLarge, len(data), topic)
	}
	if err := b.nc.Publish(subjectPrefix+topic, data); err != nil {
		return fmt.Errorf("publish to %s: %w", topic, err)
	}
	metrics.PubSubPublished.Inc()
	return nil
}

// Subscribe returns once the server has registered the subscription.
func (b *NATS) Subscribe(ctx context.Context, topic string) (*Subscription, error) {
	s := newSubscription()
	ns, err := b.nc.Subscribe(subjectPrefix+topic, func(m *nats.Msg) { s.deliver(m.Data) })
	if err != nil {
		if errors.Is(err, nats.ErrConnectionClosed) {
			err = ErrClosed
		}
		return nil, fmt.Errorf("subscribe to %s: %w", topic, err)
	}
	if !b.subs.add(s, func() { ns.Unsubscribe() }) {
		return nil, ErrClosed
	}
	// A PING/PONG round trip guarantees the server processed the SUB, so
	// nothing published after this returns is missed.
	fctx, cancel := context.WithTimeout(ctx, subscribeTimeout)
	defer cancel()
	if err := b.nc.FlushWithContext(fctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("subscribe to %s: %w", topic, err)
	}
	return s, nil
}

// Close ends all subscriptions, sends buffered events and disconnects.
func (b *NATS) Close() {
	b.subs.dropAll(true)
	if err := b.nc.FlushTimeout(2 * time.Second); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		b.log.Warn("flush pubsub on close", "error", err)
	}
	b.nc.Close()
}
