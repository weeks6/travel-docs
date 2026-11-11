package trips

import (
	"math"
	"testing"
	"time"
)

func TestValidatePosition(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f := func(v float64) *float64 { return &v }
	ok := []Position{
		{Lat: 46.5, Lon: 8.1},
		{Lat: -90, Lon: 180, Ele: f(-10), AccuracyM: f(5), SpeedMps: f(1.2), HeadingDeg: f(359.9)},
		{Lat: 0, Lon: 0, RecordedAt: now.Add(-23 * time.Hour)},
		{Lat: 0, Lon: 0, RecordedAt: now.Add(time.Minute)},
	}
	for _, p := range ok {
		if err := ValidatePosition(&p, now); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
		if p.RecordedAt.IsZero() {
			t.Errorf("%+v: recorded_at must default to now", p)
		}
	}
	bad := []Position{
		{Lat: 91, Lon: 0},
		{Lat: 0, Lon: -180.1},
		{Lat: math.NaN(), Lon: 0},
		{Lat: 0, Lon: 0, SpeedMps: f(-1)},
		{Lat: 0, Lon: 0, HeadingDeg: f(400)},
		{Lat: 0, Lon: 0, Ele: f(math.Inf(1))},
		{Lat: 0, Lon: 0, RecordedAt: now.Add(10 * time.Minute)},
		{Lat: 0, Lon: 0, RecordedAt: now.Add(-25 * time.Hour)},
	}
	for _, p := range bad {
		if err := ValidatePosition(&p, now); err == nil {
			t.Errorf("%+v: expected an error", p)
		}
	}
}
