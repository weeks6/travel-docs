package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State remembers what was exported, so runs only send new or changed
// routes. It is a JSON file owned by the scraper: the app's database stays
// the app's own.
type State struct {
	path    string
	Records map[string]*Record `json:"records"` // by Track.SourceID
	// Cursor is the next region to scan when runs take turns over the
	// regions (-regions-per-run).
	Cursor int `json:"cursor"`
}

// Record is the outcome for one source track.
type Record struct {
	RouteID     string    `json:"route_id,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"` // of the exported geometry
	Rejected    string    `json:"rejected,omitempty"`    // validation failure, if any
	UpdatedAt   time.Time `json:"updated_at"`
	// Where the exported route starts and ends, and how long it is: to
	// recognize the same trail coming from another source.
	Start   *Point  `json:"start,omitempty"`
	End     *Point  `json:"end,omitempty"`
	LengthM float64 `json:"length_m,omitempty"`
}

// Duplicate tolerances: another source's route starting and ending this
// close, with a length this similar, is the same trail.
const (
	dupEndM      = 150
	dupLengthPct = 0.15
)

// DuplicateOf returns the source ID of an exported route from another
// source that is the same trail as t (same ends, either direction, and
// about the same length), or "".
func (s *State) DuplicateOf(t Track) string {
	start, end := t.Ends()
	length := t.LengthM()
	src := sourceOf(t.SourceID)
	for id, r := range s.Records {
		if r.RouteID == "" || r.Start == nil || r.End == nil || sourceOf(id) == src {
			continue
		}
		if abs(r.LengthM-length) > dupLengthPct*max(r.LengthM, length) {
			continue
		}
		same := haversine(start, *r.Start) <= dupEndM && haversine(end, *r.End) <= dupEndM
		reversed := haversine(start, *r.End) <= dupEndM && haversine(end, *r.Start) <= dupEndM
		if same || reversed {
			return id
		}
	}
	return ""
}

// sourceOf is the source part of a source ID ("osm" in "osm:relation:1").
func sourceOf(id string) string {
	src, _, _ := strings.Cut(id, ":")
	return src
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func LoadState(path string) (*State, error) {
	s := &State{path: path, Records: map[string]*Record{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Records == nil {
		s.Records = map[string]*Record{}
	}
	return s, nil
}

// Save writes the file atomically (temp file and rename), so a crash never
// leaves it half-written.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
