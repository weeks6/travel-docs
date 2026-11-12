// Command scraper finds publicly available routes, validates them and
// exports them into Travel Docs through its public API. It is a separate
// service: its own module, process, state and schedule; it knows the app
// only as an HTTP API.
//
// Pipeline: sources discover tracks → validate → skip unchanged (state
// file) → export (background import, then attribution).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
)

// Point is a WGS84 position.
type Point struct{ Lat, Lon float64 }

// Track is a route found by a source, ready to validate and export.
type Track struct {
	SourceID    string // stable across runs, e.g. "osm:relation:1722728"
	Name        string
	Description string
	URL         string // where it was found
	Attribution string // license notice the source requires
	NeedsFix    bool   // the source marks the data as incomplete or wrong
	Segments    [][]Point
}

// Source discovers public routes in a region. A route crossing the region
// is returned whole. Implementations must respect the provider's terms of
// use and rate limits, and return nothing for regions they don't cover.
type Source interface {
	Name() string
	Tracks(ctx context.Context, r Region) ([]Track, error)
}

// Fingerprint identifies the geometry: when it changes, the route is
// exported again (as a new version).
func (t Track) Fingerprint() string {
	h := sha256.New()
	for _, seg := range t.Segments {
		for _, p := range seg {
			fmt.Fprintf(h, "%.5f,%.5f;", p.Lat, p.Lon) // ~1 m precision
		}
		h.Write([]byte{'|'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LengthM is the total length of all segments in meters.
func (t Track) LengthM() float64 {
	total := 0.0
	for _, seg := range t.Segments {
		for i := 1; i < len(seg); i++ {
			total += haversine(seg[i-1], seg[i])
		}
	}
	return total
}

// Ends returns the first point of the first segment and the last point of
// the last one. The track must have geometry.
func (t Track) Ends() (Point, Point) {
	first, last := t.Segments[0], t.Segments[len(t.Segments)-1]
	return first[0], last[len(last)-1]
}

// PointCount is the number of points in all segments.
func (t Track) PointCount() int {
	n := 0
	for _, seg := range t.Segments {
		n += len(seg)
	}
	return n
}

func haversine(a, b Point) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLon := (b.Lon - a.Lon) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// joinSegments joins line pieces given in any order and direction into as
// few lines as possible: starting from the longest piece, it keeps
// attaching a piece whose end is within tolM meters of either end of the
// current line, then starts a new line from the longest piece left.
// Datasets that store a trail as separate segments (often in no particular
// order) are put back together with it.
func joinSegments(pieces [][]Point, tolM float64) [][]Point {
	var left [][]Point
	for _, p := range pieces {
		if len(p) >= 2 {
			left = append(left, p)
		}
	}
	length := func(p []Point) float64 { return Track{Segments: [][]Point{p}}.LengthM() }
	var out [][]Point
	for len(left) > 0 {
		longest := 0
		for i := range left {
			if length(left[i]) > length(left[longest]) {
				longest = i
			}
		}
		cur := append([]Point(nil), left[longest]...)
		left = append(left[:longest], left[longest+1:]...)
		for attached := true; attached; {
			attached = false
			for i, p := range left {
				head, tail := cur[0], cur[len(cur)-1]
				switch {
				case haversine(tail, p[0]) <= tolM:
					cur = append(cur, p[1:]...)
				case haversine(tail, p[len(p)-1]) <= tolM:
					q := append([]Point(nil), p...)
					reverse(q)
					cur = append(cur, q[1:]...)
				case haversine(head, p[len(p)-1]) <= tolM:
					cur = append(append([]Point(nil), p[:len(p)-1]...), cur...)
				case haversine(head, p[0]) <= tolM:
					q := append([]Point(nil), p...)
					reverse(q)
					cur = append(q[:len(q)-1], cur...)
				default:
					continue
				}
				left = append(left[:i], left[i+1:]...)
				attached = true
				break
			}
		}
		out = append(out, cur)
	}
	return out
}
