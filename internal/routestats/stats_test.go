package routestats

import (
	"math"
	"testing"

	"github.com/weeks6/travel-docs/internal/gpx"
)

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", name, got, want, tol)
	}
}

func pt(lat, lon, ele float64) gpx.Point {
	return gpx.Point{Lat: lat, Lon: lon, Ele: ele, HasEle: true}
}

func TestHaversine(t *testing.T) {
	// One degree of latitude along a meridian = 2πR/360.
	approx(t, "1° lat", Haversine(0, 0, 1, 0), 2*math.Pi*EarthRadiusM/360, 0.01)
	approx(t, "same point", Haversine(46.5, 8, 46.5, 8), 0, 0)
	// Paris → London, roughly 343.5 km.
	approx(t, "Paris-London", Haversine(48.8566, 2.3522, 51.5074, -0.1278), 343_500, 1_000)
	// Antipodal points are half the circumference apart.
	approx(t, "antipodes", Haversine(0, 0, 0, 180), math.Pi*EarthRadiusM, 0.01)
}

func TestComputeDistanceSkipsSegmentGaps(t *testing.T) {
	segs := []gpx.Segment{
		{{Lat: 0, Lon: 0}, {Lat: 1, Lon: 0}},
		{{Lat: 50, Lon: 50}, {Lat: 51, Lon: 50}}, // far away; the jump must not count
	}
	s := Compute(segs)
	approx(t, "distance", s.DistanceM, 2*Haversine(0, 0, 1, 0), 0.01)
	if s.MinElevationM != nil || s.MaxElevationM != nil {
		t.Error("expected nil min/max elevation without elevation data")
	}
	if s.AscentM != 0 || s.DescentM != 0 {
		t.Errorf("ascent/descent = %v/%v, want 0", s.AscentM, s.DescentM)
	}
	if s.Profile == nil || len(s.Profile) != 0 {
		t.Errorf("profile = %#v, want empty non-nil slice", s.Profile)
	}
}

func TestComputeAscentDescent(t *testing.T) {
	seg := gpx.Segment{
		pt(0, 0, 100), pt(0, 0.001, 110), pt(0, 0.002, 150), // +50
		pt(0, 0.003, 120), pt(0, 0.004, 100), // -50
		pt(0, 0.005, 130), // +30
	}
	s := Compute([]gpx.Segment{seg})
	approx(t, "ascent", s.AscentM, 80, 1e-9)
	approx(t, "descent", s.DescentM, 50, 1e-9)
	approx(t, "min", *s.MinElevationM, 100, 0)
	approx(t, "max", *s.MaxElevationM, 150, 0)

	if len(s.Profile) != len(seg) {
		t.Fatalf("profile has %d points, want %d", len(s.Profile), len(seg))
	}
	if s.Profile[0].DistanceM != 0 {
		t.Errorf("profile starts at %v, want 0", s.Profile[0].DistanceM)
	}
	approx(t, "profile end", s.Profile[len(s.Profile)-1].DistanceM, s.DistanceM, 1e-9)
}

func TestComputeIgnoresNoise(t *testing.T) {
	// Jitter of ±1 m around a flat line should not add any ascent.
	var seg gpx.Segment
	for i := range 100 {
		ele := 500.0
		if i%2 == 1 {
			ele = 501
		}
		seg = append(seg, pt(0, float64(i)*0.0001, ele))
	}
	s := Compute([]gpx.Segment{seg})
	if s.AscentM != 0 || s.DescentM != 0 {
		t.Errorf("ascent/descent = %v/%v, want 0 for noise below threshold", s.AscentM, s.DescentM)
	}
}

func TestComputeGradualClimbIsCounted(t *testing.T) {
	// 1 m steps are below the threshold individually but add up.
	var seg gpx.Segment
	for i := range 31 {
		seg = append(seg, pt(0, float64(i)*0.0001, 100+float64(i)))
	}
	s := Compute([]gpx.Segment{seg})
	approx(t, "ascent", s.AscentM, 30, 1e-9)
}

func TestComputeMixedElevation(t *testing.T) {
	seg := gpx.Segment{pt(0, 0, 100), {Lat: 0, Lon: 0.001}, pt(0, 0.002, 120)}
	s := Compute([]gpx.Segment{seg})
	if len(s.Profile) != 2 {
		t.Errorf("profile has %d points, want 2 (points without elevation skipped)", len(s.Profile))
	}
	approx(t, "ascent", s.AscentM, 20, 1e-9)
}

func TestDownsample(t *testing.T) {
	pts := make([]ProfilePoint, 10_001)
	for i := range pts {
		pts[i] = ProfilePoint{DistanceM: float64(i)}
	}
	out := Downsample(pts, 1000)
	if len(out) != 1000 {
		t.Fatalf("len = %d, want 1000", len(out))
	}
	if out[0].DistanceM != 0 || out[999].DistanceM != 10_000 {
		t.Errorf("first/last = %v/%v, want 0/10000", out[0].DistanceM, out[999].DistanceM)
	}
	for i := 1; i < len(out); i++ {
		if out[i].DistanceM <= out[i-1].DistanceM {
			t.Fatalf("not strictly increasing at %d", i)
		}
	}
	if got := Downsample(pts[:5], 1000); len(got) != 5 {
		t.Errorf("short input changed: len %d", len(got))
	}
}
