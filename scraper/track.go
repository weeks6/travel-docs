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

// Source discovers public routes. Implementations must respect the
// provider's terms of use and rate limits.
type Source interface {
	Name() string
	Tracks(ctx context.Context) ([]Track, error)
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
