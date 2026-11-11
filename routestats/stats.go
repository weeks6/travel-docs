// Package routestats computes distance and elevation statistics for GPX tracks.
package routestats

import (
	"math"

	"github.com/weeks6/travel-docs/gpx"
)

// EarthRadiusM is the mean Earth radius in meters.
const EarthRadiusM = 6371008.8

// ElevationThresholdM filters GPS/barometer noise when summing ascent and
// descent: a climb or drop is only counted once it exceeds this many meters
// relative to the last counted elevation.
const ElevationThresholdM = 3.0

// MaxProfilePoints caps the size of the elevation profile.
const MaxProfilePoints = 1000

// ProfilePoint is one sample of the elevation profile.
type ProfilePoint struct {
	DistanceM  float64 `json:"distance_m"`
	ElevationM float64 `json:"elevation_m"`
}

// Stats holds the computed route statistics.
type Stats struct {
	DistanceM     float64
	AscentM       float64
	DescentM      float64
	MinElevationM *float64 // nil when the file has no elevation data
	MaxElevationM *float64
	Profile       []ProfilePoint
}

// Haversine returns the great-circle distance in meters between two points.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	dφ := (lat2 - lat1) * math.Pi / 180
	dλ := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Compute calculates distance, ascent, descent and the elevation profile.
// Distance is not counted across gaps between segments; the profile's
// distance axis is cumulative over all segments.
func Compute(segments []gpx.Segment) Stats {
	var s Stats
	var profile []ProfilePoint
	var minEle, maxEle float64
	hasEle := false

	for _, seg := range segments {
		var ref float64 // last elevation counted towards ascent/descent
		refSet := false
		for i, p := range seg {
			if i > 0 {
				prev := seg[i-1]
				s.DistanceM += Haversine(prev.Lat, prev.Lon, p.Lat, p.Lon)
			}
			if !p.HasEle {
				continue
			}

			if !hasEle {
				minEle, maxEle, hasEle = p.Ele, p.Ele, true
			}
			minEle = math.Min(minEle, p.Ele)
			maxEle = math.Max(maxEle, p.Ele)
			profile = append(profile, ProfilePoint{DistanceM: s.DistanceM, ElevationM: p.Ele})

			if !refSet {
				ref, refSet = p.Ele, true
				continue
			}
			switch d := p.Ele - ref; {
			case d >= ElevationThresholdM:
				s.AscentM += d
				ref = p.Ele
			case d <= -ElevationThresholdM:
				s.DescentM -= d
				ref = p.Ele
			}
		}
	}

	if hasEle {
		s.MinElevationM = &minEle
		s.MaxElevationM = &maxEle
	}
	s.Profile = Downsample(profile, MaxProfilePoints)
	return s
}

// Downsample keeps at most max points, evenly spaced, always keeping the
// first and the last point.
func Downsample(pts []ProfilePoint, max int) []ProfilePoint {
	if pts == nil {
		return []ProfilePoint{}
	}
	if len(pts) <= max || max < 2 {
		return pts
	}
	out := make([]ProfilePoint, 0, max)
	step := float64(len(pts)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		out = append(out, pts[int(math.Round(float64(i)*step))])
	}
	return out
}
