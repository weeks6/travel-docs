// Package pubsub delivers small real-time events (job progress, trip
// locations, presence) to subscribers.
//
// Two brokers are provided:
//   - NATS sends events through a NATS server (core NATS, at-most-once),
//     so every app instance connected to it receives every event;
//   - Local delivers within the process (tests, single instance).
//
// Delivery is best effort. A subscriber that falls behind, or all
// subscribers when the broker loses its connection, get their channel
// closed: they should reload the current state and subscribe again.
package pubsub

import (
	"context"
	"errors"
	"sync"

	"github.com/weeks6/travel-docs/internal/metrics"
)

// ErrClosed is returned when subscribing to a broker that was closed.
var ErrClosed = errors.New("pubsub: broker closed")

// Broker publishes events to topics and lets callers subscribe to them.
type Broker interface {
	// Publish sends data to all current subscribers of topic.
	Publish(ctx context.Context, topic string, data []byte) error
	// Subscribe starts receiving events of topic. Every event published
	// after Subscribe returns is received (or the subscription is closed).
	// Call Close when done.
	Subscribe(ctx context.Context, topic string) (*Subscription, error)
}

// SubscriptionBuffer is how many undelivered events a subscriber may have
// before it is considered too slow and its subscription is closed.
const SubscriptionBuffer = 256

// Subscription receives the events of one topic.
type Subscription struct {
	// C receives event payloads. It is closed when the subscription ends:
	// after Close, when the subscriber is too slow, or when the broker
	// stops or loses its connection. Events may have been missed in the
	// latter cases.
	C <-chan []byte

	mu      sync.Mutex
	c       chan []byte
	done    bool
	cleanup func()
}

func newSubscription() *Subscription {
	c := make(chan []byte, SubscriptionBuffer)
	return &Subscription{C: c, c: c}
}

// Close ends the subscription. It is safe to call more than once and
// concurrently with deliveries.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	close(s.c)
	cleanup := s.cleanup
	s.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

// onClose sets the function that releases the subscription's resources.
// It runs right away if the subscription has already ended.
func (s *Subscription) onClose(f func()) {
	s.mu.Lock()
	if !s.done {
		s.cleanup = f
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	f()
}

// deliver passes data on without blocking. A subscriber whose buffer is
// full is too slow and is dropped.
func (s *Subscription) deliver(data []byte) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	select {
	case s.c <- data:
		s.mu.Unlock()
		metrics.PubSubDelivered.Inc()
		return
	default:
	}
	s.mu.Unlock()
	metrics.PubSubDroppedSubscribers.Inc()
	s.Close()
}

// subscriptionSet tracks open subscriptions so they can all be dropped.
type subscriptionSet struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool
}

// add registers s and arranges for its removal on Close. It reports false
// (and closes s) when the set was closed.
func (set *subscriptionSet) add(s *Subscription, release func()) bool {
	set.mu.Lock()
	if set.closed {
		set.mu.Unlock()
		s.Close()
		if release != nil {
			release()
		}
		return false
	}
	if set.subs == nil {
		set.subs = map[*Subscription]struct{}{}
	}
	set.subs[s] = struct{}{}
	set.mu.Unlock()
	s.onClose(func() {
		set.mu.Lock()
		delete(set.subs, s)
		set.mu.Unlock()
		if release != nil {
			release()
		}
	})
	return true
}

// dropAll closes every subscription; with stop, the set accepts no more.
func (set *subscriptionSet) dropAll(stop bool) {
	set.mu.Lock()
	subs := make([]*Subscription, 0, len(set.subs))
	for s := range set.subs {
		subs = append(subs, s)
	}
	if stop {
		set.closed = true
	}
	set.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}

// Local is an in-process broker.
type Local struct {
	mu     sync.RWMutex
	topics map[string]map[*Subscription]struct{}
	all    subscriptionSet
}

func NewLocal() *Local { return &Local{topics: map[string]map[*Subscription]struct{}{}} }

func (l *Local) Publish(_ context.Context, topic string, data []byte) error {
	metrics.PubSubPublished.Inc()
	l.mu.RLock()
	subs := make([]*Subscription, 0, len(l.topics[topic]))
	for s := range l.topics[topic] {
		subs = append(subs, s)
	}
	l.mu.RUnlock()
	for _, s := range subs {
		s.deliver(data)
	}
	return nil
}

func (l *Local) Subscribe(_ context.Context, topic string) (*Subscription, error) {
	s := newSubscription()
	l.mu.Lock()
	m := l.topics[topic]
	if m == nil {
		m = map[*Subscription]struct{}{}
		l.topics[topic] = m
	}
	m[s] = struct{}{}
	l.mu.Unlock()
	ok := l.all.add(s, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.topics[topic], s)
		if len(l.topics[topic]) == 0 {
			delete(l.topics, topic)
		}
	})
	if !ok {
		return nil, ErrClosed
	}
	return s, nil
}

// Close ends all subscriptions; later Subscribe calls fail.
func (l *Local) Close() { l.all.dropAll(true) }

// UserTopic carries events for one user (import progress, invitations).
func UserTopic(userID string) string { return "user." + userID }

// TripTopic carries a trip's live events (locations, presence, changes).
func TripTopic(tripID string) string { return "trip." + tripID }
