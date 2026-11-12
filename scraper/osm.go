package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OSMSource finds hiking and cycling route relations in OpenStreetMap
// through the Overpass API, anywhere in the world. OSM data is open (ODbL)
// and may be reused with attribution; Overpass asks for modest use, so
// queries are per region, sequential, with backoff on load errors.
type OSMSource struct {
	Endpoint  string   // Overpass interpreter URL
	Routes    []string // OSM route types, e.g. hiking, bicycle
	UserAgent string
	HTTP      *http.Client
}

// BBox is south, west, north, east in degrees.
type BBox struct{ South, West, North, East float64 }

// ParseBBox reads "south,west,north,east".
func ParseBBox(s string) (BBox, error) {
	var b BBox
	if _, err := fmt.Sscanf(strings.ReplaceAll(s, " ", ""), "%f,%f,%f,%f", &b.South, &b.West, &b.North, &b.East); err != nil {
		return b, fmt.Errorf("bbox %q: want south,west,north,east", s)
	}
	if b.South >= b.North || b.West >= b.East || b.South < -90 || b.North > 90 || b.West < -180 || b.East > 180 {
		return b, fmt.Errorf("bbox %q: south < north and west < east, within -90..90 and -180..180", s)
	}
	return b, nil
}

func (s *OSMSource) Name() string { return "osm" }

func (s *OSMSource) Tracks(ctx context.Context, r Region) ([]Track, error) {
	rels, err := s.query(ctx, r.BBox)
	if err != nil {
		return nil, err
	}
	out := make([]Track, len(rels))
	for i, rel := range rels {
		out[i] = rel.track()
	}
	return out, nil
}

type osmResponse struct {
	Elements []osmRelation `json:"elements"`
}

type osmRelation struct {
	Type    string            `json:"type"`
	ID      int64             `json:"id"`
	Tags    map[string]string `json:"tags"`
	Members []struct {
		Type     string  `json:"type"`
		Role     string  `json:"role"`
		Geometry []Point `json:"geometry"`
	} `json:"members"`
}

func (p *Point) UnmarshalJSON(b []byte) error {
	var v struct{ Lat, Lon float64 }
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Lat, p.Lon = v.Lat, v.Lon
	return nil
}

func (s *OSMSource) query(ctx context.Context, b BBox) ([]osmRelation, error) {
	routes := strings.Join(s.Routes, "|")
	q := fmt.Sprintf(`[out:json][timeout:180];relation["type"="route"]["route"~"^(%s)$"]["name"](%f,%f,%f,%f);out body geom;`,
		routes, b.South, b.West, b.North, b.East)
	var res osmResponse
	backoff := 10 * time.Second
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", s.Endpoint, strings.NewReader(url.Values{"data": {q}}.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", s.UserAgent)
		resp, err := s.HTTP.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case rerr != nil:
				err = rerr
			case resp.StatusCode == http.StatusOK:
				if jerr := json.Unmarshal(body, &res); jerr != nil {
					// Overpass sometimes answers 200 with an HTML error page under load.
					err = fmt.Errorf("unexpected response: %.120s", body)
				} else {
					return res.Elements, nil
				}
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				err = fmt.Errorf("overpass busy (HTTP %d)", resp.StatusCode)
			default:
				return nil, fmt.Errorf("overpass: HTTP %d: %.200s", resp.StatusCode, body)
			}
		}
		if attempt == 4 {
			return nil, fmt.Errorf("overpass query for %v: %w", b, err)
		}
		if err := sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
	}
}

// track joins the relation's ways into as few lines as possible: OSM stores
// a route as ways in rough order, each possibly drawn in either direction.
// Alternative and excursion branches are left out.
func (r osmRelation) track() Track {
	var segs [][]Point
	flippable := false // the last line is a single way, so it may still be reversed
	for _, m := range r.Members {
		if m.Type != "way" || len(m.Geometry) < 2 || strings.Contains(m.Role, "alternative") || strings.Contains(m.Role, "excursion") {
			continue
		}
		way := append([]Point(nil), m.Geometry...)
		if n := len(segs); n > 0 {
			last := segs[n-1]
			first, end := last[0], last[len(last)-1]
			joined := true
			switch {
			case way[0] == end:
			case way[len(way)-1] == end:
				reverse(way)
			case flippable && way[0] == first:
				reverse(last)
			case flippable && way[len(way)-1] == first:
				reverse(last)
				reverse(way)
			default:
				joined = false
			}
			if joined {
				segs[n-1] = append(last, way[1:]...)
				flippable = false
				continue
			}
		}
		segs = append(segs, way)
		flippable = true
	}
	desc := strings.TrimSpace(r.Tags["description"])
	if note := routeNote(r.Tags); note != "" {
		desc = strings.TrimSpace(note + "\n\n" + desc)
	}
	return Track{
		SourceID:    fmt.Sprintf("osm:relation:%d", r.ID),
		Name:        strings.TrimSpace(r.Tags["name"]),
		Description: desc,
		URL:         fmt.Sprintf("https://www.openstreetmap.org/relation/%d", r.ID),
		Attribution: "Route data © OpenStreetMap contributors, available under the Open Database License (ODbL).",
		// Mappers flag known problems with a fixme tag or "fixme" in the name.
		NeedsFix: r.Tags["fixme"] != "" || r.Tags["FIXME"] != "" || strings.Contains(strings.ToLower(r.Tags["name"]), "fixme"),
		Segments: segs,
	}
}

// routeNote summarizes useful route tags, e.g. "Hiking route (regional network) from A to B".
func routeNote(t map[string]string) string {
	kind := map[string]string{"hiking": "Hiking", "foot": "Walking", "bicycle": "Cycling", "mtb": "Mountain bike"}[t["route"]]
	if kind == "" {
		kind = "Route"
	}
	net := map[string]string{"iwn": "international", "nwn": "national", "rwn": "regional", "lwn": "local",
		"icn": "international", "ncn": "national", "rcn": "regional", "lcn": "local"}[t["network"]]
	s := kind + " route"
	if net != "" {
		s += " (" + net + " network)"
	}
	if t["from"] != "" && t["to"] != "" {
		s += " from " + t["from"] + " to " + t["to"]
	}
	return s + "."
}

func reverse(p []Point) {
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
}
