package main

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Rules decide which tracks are worth exporting. Tracks that break them are
// skipped and counted by reason; the app validates the GPX again on import.
type Rules struct {
	MinPoints   int     // fewer points: probably a stub
	MinLengthM  float64 // shorter: not a route
	MaxLengthM  float64 // longer: long-distance relations, too big to be useful as one route
	MaxSegments int     // more pieces: a broken or unfinished relation
	MaxGapM     float64 // a jump between two consecutive points this long means bad data
	MinMainPart float64 // share of the length in the longest piece (0–1)
}

func DefaultRules() Rules {
	return Rules{MinPoints: 10, MinLengthM: 1000, MaxLengthM: 1_000_000, MaxSegments: 20, MaxGapM: 2000, MinMainPart: 0.5}
}

// Validate cleans up the track (drops empty segments, trims the name) and
// returns why it's rejected, or "" when it's fine.
func (r Rules) Validate(t *Track) string {
	t.Name = strings.Join(strings.Fields(t.Name), " ")
	if t.Name == "" {
		return "no name"
	}
	if t.NeedsFix {
		return "marked as needing fixes at the source"
	}
	if utf8.RuneCountInString(t.Name) > 200 {
		t.Name = string([]rune(t.Name)[:200])
	}
	segs := t.Segments[:0]
	for _, s := range t.Segments {
		if len(s) >= 2 {
			segs = append(segs, s)
		}
	}
	t.Segments = segs
	if len(t.Segments) == 0 {
		return "no geometry"
	}
	for _, s := range t.Segments {
		for i, p := range s {
			if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
				return "invalid coordinates"
			}
			if i > 0 && haversine(s[i-1], p) > r.MaxGapM {
				return fmt.Sprintf("jump over %.0f km between points", r.MaxGapM/1000)
			}
		}
	}
	if t.PointCount() < r.MinPoints {
		return fmt.Sprintf("fewer than %d points", r.MinPoints)
	}
	length := t.LengthM()
	switch {
	case length < r.MinLengthM:
		return fmt.Sprintf("shorter than %.0f km", r.MinLengthM/1000)
	case length > r.MaxLengthM:
		return fmt.Sprintf("longer than %.0f km", r.MaxLengthM/1000)
	case len(t.Segments) > r.MaxSegments:
		return fmt.Sprintf("more than %d separate pieces", r.MaxSegments)
	}
	longest := 0.0
	for _, s := range t.Segments {
		longest = math.Max(longest, Track{Segments: [][]Point{s}}.LengthM())
	}
	if longest/length < r.MinMainPart {
		return "too fragmented"
	}
	return ""
}
