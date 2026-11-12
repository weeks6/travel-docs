package imports

import (
	"testing"
	"time"
)

func TestLooksLikeXML(t *testing.T) {
	for in, want := range map[string]bool{
		"<gpx/>": true, "\xef\xbb\xbf  \n<?xml?>": true, "": false, "{\"type\":1}": false, "   ": false,
	} {
		if got := looksLikeXML([]byte(in)); got != want {
			t.Errorf("looksLikeXML(%q) = %v", in, got)
		}
	}
}

func TestRequestHashCoversTheRequest(t *testing.T) {
	base := NewJob{Kind: KindCreate, Filename: "a.gpx", Name: "A", Visibility: "private", Payload: []byte("<gpx/>")}
	variants := []NewJob{base, base, base, base, base}
	variants[1].Payload = []byte("<gpx />")
	variants[2].Name = "B"
	variants[3].Visibility = "public"
	variants[4].Kind, variants[4].RouteID, variants[4].IfVersion = KindReplaceGeometry, "r", 3
	seen := map[string]int{}
	for i, v := range variants {
		h := string(v.requestHash())
		if j, ok := seen[h]; ok {
			t.Errorf("variants %d and %d have the same hash", j, i)
		}
		seen[h] = i
	}
	if string(base.requestHash()) != string(base.requestHash()) {
		t.Error("hash is not deterministic")
	}
}

func TestRetryDelayGrowsAndIsCapped(t *testing.T) {
	r := &Runner{cfg: Config{RetryBaseDelay: time.Second}}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 20: time.Minute} {
		got := r.retryDelay(attempt)
		if got < want*8/10 || got > want*12/10 {
			t.Errorf("retryDelay(%d) = %v, want %v ± 20%%", attempt, got, want)
		}
	}
}
