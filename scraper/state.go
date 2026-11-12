package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State remembers what was exported, so runs only send new or changed
// routes. It is a JSON file owned by the scraper: the app's database stays
// the app's own.
type State struct {
	path    string
	Records map[string]*Record `json:"records"` // by Track.SourceID
}

// Record is the outcome for one source track.
type Record struct {
	RouteID     string    `json:"route_id,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"` // of the exported geometry
	Rejected    string    `json:"rejected,omitempty"`    // validation failure, if any
	UpdatedAt   time.Time `json:"updated_at"`
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
