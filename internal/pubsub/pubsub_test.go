package pubsub_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/weeks6/travel-docs/internal/pubsub"
	"github.com/weeks6/travel-docs/internal/pubsub/natstest"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// brokers runs a test against each broker implementation.
func brokers(t *testing.T, test func(t *testing.T, b pubsub.Broker)) {
	t.Run("local", func(t *testing.T) {
		b := pubsub.NewLocal()
		defer b.Close()
		test(t, b)
	})
	t.Run("nats", func(t *testing.T) {
		srv := natstest.Run(t)
		b, err := pubsub.ConnectNATS(srv.URL(), discard)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		test(t, b)
	})
}

func subscribe(t *testing.T, b pubsub.Broker, topic string) *pubsub.Subscription {
	t.Helper()
	s, err := b.Subscribe(context.Background(), topic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func receive(t *testing.T, s *pubsub.Subscription) string {
	t.Helper()
	select {
	case got, ok := <-s.C:
		if !ok {
			t.Fatal("subscription closed")
		}
		return string(got)
	case <-time.After(2 * time.Second):
		t.Fatal("event not delivered")
	}
	return ""
}

func waitClosed(t *testing.T, s *pubsub.Subscription) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-s.C:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("subscription not closed")
		}
	}
}

func TestDeliversToTopicSubscribers(t *testing.T) {
	brokers(t, func(t *testing.T, b pubsub.Broker) {
		a1, a2, other := subscribe(t, b, "a"), subscribe(t, b, "a"), subscribe(t, b, "b")
		if err := b.Publish(context.Background(), "a", []byte("hello")); err != nil {
			t.Fatal(err)
		}
		for _, s := range []*pubsub.Subscription{a1, a2} {
			if got := receive(t, s); got != "hello" {
				t.Errorf("got %q", got)
			}
		}
		select {
		case got := <-other.C:
			t.Errorf("other topic received %q", got)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	brokers(t, func(t *testing.T, b pubsub.Broker) {
		slow := subscribe(t, b, "t")
		for i := 0; i <= pubsub.SubscriptionBuffer; i++ {
			b.Publish(context.Background(), "t", []byte("x"))
		}
		// Delivery may be asynchronous: don't read until the buffer is full.
		for deadline := time.Now().Add(5 * time.Second); len(slow.C) < pubsub.SubscriptionBuffer; {
			if time.Now().After(deadline) {
				t.Fatalf("only %d events buffered", len(slow.C))
			}
			time.Sleep(10 * time.Millisecond)
		}
		n := 0
		timeout := time.After(5 * time.Second)
	drain:
		for {
			select {
			case _, ok := <-slow.C:
				if !ok {
					break drain
				}
				n++
			case <-timeout:
				t.Fatal("subscription not closed")
			}
		}
		if n != pubsub.SubscriptionBuffer {
			t.Errorf("received %d buffered events, want %d", n, pubsub.SubscriptionBuffer)
		}
		slow.Close() // already closed: must not panic
	})
}

func TestCloseEndsSubscriptions(t *testing.T) {
	local := pubsub.NewLocal()
	s := subscribe(t, local, "t")
	local.Close()
	waitClosed(t, s)
	if _, err := local.Subscribe(context.Background(), "t"); !errors.Is(err, pubsub.ErrClosed) {
		t.Errorf("subscribe after Close: got %v, want ErrClosed", err)
	}

	srv := natstest.Run(t)
	nb, err := pubsub.ConnectNATS(srv.URL(), discard)
	if err != nil {
		t.Fatal(err)
	}
	s = subscribe(t, nb, "t")
	nb.Close()
	waitClosed(t, s)
	if _, err := nb.Subscribe(context.Background(), "t"); !errors.Is(err, pubsub.ErrClosed) {
		t.Errorf("subscribe after Close: got %v, want ErrClosed", err)
	}
}

// Events go to every instance, and a subscription is active on the server
// once Subscribe returns.
func TestNATSAcrossInstances(t *testing.T) {
	srv := natstest.Run(t)
	b1, err := pubsub.ConnectNATS(srv.URL(), discard)
	if err != nil {
		t.Fatal(err)
	}
	defer b1.Close()
	b2, err := pubsub.ConnectNATS(srv.URL(), discard)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()

	for i := range 20 {
		s := subscribe(t, b1, pubsub.TripTopic("x"))
		if err := b2.Publish(context.Background(), pubsub.TripTopic("x"), []byte("hi")); err != nil {
			t.Fatal(err)
		}
		if got := receive(t, s); got != "hi" {
			t.Fatalf("round %d: got %q", i, got)
		}
		s.Close()
	}
}

// After a lost connection subscribers must resync; subscribing again works
// once the client has reconnected.
func TestNATSReconnectDropsSubscriptions(t *testing.T) {
	srv := natstest.Run(t)
	b, err := pubsub.ConnectNATS(srv.URL(), discard)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s := subscribe(t, b, "t")

	srv.Restart(t)
	waitClosed(t, s)

	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err = b.Subscribe(ctx, "t")
		cancel()
		if err == nil {
			// Not dropped by a late reconnect notification?
			b.Publish(context.Background(), "t", []byte("again"))
			select {
			case got, ok := <-s.C:
				if ok && string(got) == "again" {
					s.Close()
					return
				}
			case <-time.After(time.Second):
			}
			s.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("no working subscription after reconnect: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestNATSRejectsOversizedEvents(t *testing.T) {
	srv := natstest.Run(t)
	b, err := pubsub.ConnectNATS(srv.URL(), discard)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	big := make([]byte, 2<<20)
	if err := b.Publish(context.Background(), "t", big); !errors.Is(err, pubsub.ErrTooLarge) {
		t.Errorf("got %v, want ErrTooLarge", err)
	}
}
