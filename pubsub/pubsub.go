// Package pubsub delivers small real-time events (job progress, trip
// locations, presence) to subscribers.
//
// Two brokers are provided:
//   - Local delivers within the process (tests, single instance);
//   - Postgres sends events through PostgreSQL LISTEN/NOTIFY, so every app
//     instance connected to the same database receives every event.
//
// Delivery is best effort. A subscriber that falls behind, or all
// subscribers when the broker loses its database connection, get their
// channel closed: they should reload the current state and subscribe again.
package pubsub

import (
	"context"
	"sync"

	"github.com/weeks6/travel-docs/metrics"
)

// Broker publishes events to topics and lets callers subscribe to them.
type Broker interface {
	// Publish sends data to all current subscribers of topic.
	Publish(ctx context.Context, topic string, data []byte) error
	// Subscribe starts receiving events of topic. Call Close when done.
	Subscribe(topic string) *Subscription
}

// SubscriptionBuffer is how many undelivered events a subscriber may have
// before it is considered too slow and its subscription is closed.
const SubscriptionBuffer = 256

// Subscription receives the events of one topic.
type Subscription struct {
	// C receives event payloads. It is closed when the subscription ends:
	// after Close, when the subscriber is too slow, or when the broker
	// stops or reconnects. Events may have been missed in the latter cases.
	C     <-chan []byte
	c     chan []byte
	topic string
	hub   *hub
}

// Close ends the subscription. It is safe to call more than once.
func (s *Subscription) Close() { s.hub.remove(s) }

// hub keeps the subscribers of this process and fans events out to them.
type hub struct {
	mu     sync.RWMutex
	subs   map[string]map[*Subscription]struct{}
	closed bool
}

func newHub() *hub { return &hub{subs: map[string]map[*Subscription]struct{}{}} }

func (h *hub) subscribe(topic string) *Subscription {
	c := make(chan []byte, SubscriptionBuffer)
	s := &Subscription{C: c, c: c, topic: topic, hub: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(c)
		return s
	}
	m := h.subs[topic]
	if m == nil {
		m = map[*Subscription]struct{}{}
		h.subs[topic] = m
	}
	m[s] = struct{}{}
	return s
}

func (h *hub) remove(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(s)
}

func (h *hub) removeLocked(s *Subscription) {
	m := h.subs[s.topic]
	if _, ok := m[s]; !ok {
		return
	}
	delete(m, s)
	if len(m) == 0 {
		delete(h.subs, s.topic)
	}
	close(s.c)
}

// deliver sends data to every subscriber of topic without blocking.
// Subscribers whose buffer is full are dropped.
func (h *hub) deliver(topic string, data []byte) {
	var slow []*Subscription
	h.mu.RLock()
	for s := range h.subs[topic] {
		select {
		case s.c <- data:
			metrics.PubSubDelivered.Inc()
		default:
			slow = append(slow, s)
		}
	}
	h.mu.RUnlock()
	if len(slow) == 0 {
		return
	}
	metrics.PubSubDroppedSubscribers.Add(float64(len(slow)))
	h.mu.Lock()
	for _, s := range slow {
		h.removeLocked(s)
	}
	h.mu.Unlock()
}

// dropAll closes every subscription; with stop, new subscriptions are
// closed immediately too.
func (h *hub) dropAll(stop bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.subs {
		for s := range m {
			h.removeLocked(s)
		}
	}
	if stop {
		h.closed = true
	}
}

// Local is an in-process broker.
type Local struct{ hub *hub }

func NewLocal() *Local { return &Local{hub: newHub()} }

func (l *Local) Publish(_ context.Context, topic string, data []byte) error {
	metrics.PubSubPublished.Inc()
	l.hub.deliver(topic, data)
	return nil
}

func (l *Local) Subscribe(topic string) *Subscription { return l.hub.subscribe(topic) }

// Close ends all subscriptions.
func (l *Local) Close() { l.hub.dropAll(true) }

// UserTopic carries events for one user (import progress, invitations).
func UserTopic(userID string) string { return "user:" + userID }

// TripTopic carries a trip's live events (locations, presence, changes).
func TripTopic(tripID string) string { return "trip:" + tripID }
