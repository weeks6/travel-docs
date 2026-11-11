package pubsub

import (
	"context"
	"testing"
	"time"
)

func TestLocalDeliversToTopicSubscribers(t *testing.T) {
	b := NewLocal()
	defer b.Close()
	a1, a2, other := b.Subscribe("a"), b.Subscribe("a"), b.Subscribe("b")
	defer a1.Close()
	defer a2.Close()
	defer other.Close()

	b.Publish(context.Background(), "a", []byte("hello"))
	for _, s := range []*Subscription{a1, a2} {
		select {
		case got := <-s.C:
			if string(got) != "hello" {
				t.Errorf("got %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("event not delivered")
		}
	}
	select {
	case got := <-other.C:
		t.Errorf("other topic received %q", got)
	default:
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	b := NewLocal()
	defer b.Close()
	slow := b.Subscribe("t")
	for i := 0; i <= SubscriptionBuffer; i++ {
		b.Publish(context.Background(), "t", []byte("x"))
	}
	n := 0
	for range slow.C { // closed after the buffered events
		n++
	}
	if n != SubscriptionBuffer {
		t.Errorf("received %d buffered events, want %d", n, SubscriptionBuffer)
	}
	slow.Close() // already closed: must not panic
}

func TestCloseEndsSubscriptions(t *testing.T) {
	b := NewLocal()
	s := b.Subscribe("t")
	b.Close()
	if _, ok := <-s.C; ok {
		t.Error("subscription still open after broker Close")
	}
	late := b.Subscribe("t")
	if _, ok := <-late.C; ok {
		t.Error("subscribing to a closed broker must return a closed subscription")
	}
}
