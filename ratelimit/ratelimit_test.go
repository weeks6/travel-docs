package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := New(1, 2) // 1/s, burst 2
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("event %d within the burst was rejected", i+1)
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("third event: ok=%v wait=%v, want rejected with wait ≤ 1s", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Error("keys must have separate buckets")
	}
	now = now.Add(time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Error("a token must be available again after a second")
	}
}

func TestNilLimiterAllowsEverything(t *testing.T) {
	var l *Limiter = New(0, 10)
	if l != nil {
		t.Fatal("rate 0 must disable the limiter")
	}
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatal("nil limiter rejected an event")
		}
	}
}

func TestIdleBucketsAreSwept(t *testing.T) {
	l := New(1, 1)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	l.Allow("a")
	now = now.Add(2 * idleTTL)
	l.Allow("b")
	if _, ok := l.buckets["a"]; ok {
		t.Error("idle bucket was not removed")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{0: 1, 300 * time.Millisecond: 1, 1500 * time.Millisecond: 2, 10 * time.Hour: 3600} {
		if got := RetryAfterSeconds(d); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}
