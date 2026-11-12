package routes

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/gpx"
	"github.com/weeks6/travel-docs/internal/routestats"
	"github.com/weeks6/travel-docs/internal/textutil"
)

const (
	maxNameLength        = 200
	maxDescriptionLength = 5000
	// MaxGeoJSONPoints limits geometry edits sent as GeoJSON.
	MaxGeoJSONPoints = 200_000
)

// BuildContent computes stats and geometry for a parsed file. Missing
// elevations are interpolated so that stored geometry is always 3D.
// The name is taken from nameOverride, then the GPX file, then the file name.
func BuildContent(f *gpx.File, filename, nameOverride string) (Content, error) {
	hasEle := f.FillElevation()
	stats := routestats.Compute(f.Segments)

	lines := make([][][3]float64, 0, len(f.Segments))
	for _, seg := range f.Segments {
		line := make([][3]float64, len(seg))
		for i, p := range seg {
			line[i] = [3]float64{p.Lon, p.Lat, p.Ele}
		}
		lines = append(lines, line)
	}
	geom, err := json.Marshal(map[string]any{"type": "MultiLineString", "coordinates": lines})
	if err != nil {
		return Content{}, err
	}

	filename = CleanFilename(filename)
	name := strings.TrimSpace(nameOverride)
	if name == "" {
		name = f.Name
	}
	if name == "" {
		name = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	if name == "" {
		name = "Untitled route"
	}

	c := Content{
		Name:             textutil.Truncate(name, maxNameLength),
		Description:      textutil.Truncate(f.Description, maxDescriptionLength),
		OriginalFilename: filename,
		DistanceM:        stats.DistanceM,
		AscentM:          stats.AscentM,
		DescentM:         stats.DescentM,
		MinElevationM:    stats.MinElevationM,
		MaxElevationM:    stats.MaxElevationM,
		HasElevation:     hasEle,
		PointCount:       f.PointCount(),
		Geometry:         geom,
		Profile:          stats.Profile,
	}
	if t, ok := f.StartTime(); ok {
		t = t.UTC()
		c.StartedAt = &t
	}
	return c, nil
}

// ParseGeoJSON reads an edited route geometry: a LineString or
// MultiLineString, either bare or wrapped in a Feature. Coordinates are
// [lon, lat] or [lon, lat, elevation].
func ParseGeoJSON(data []byte) (*gpx.File, error) {
	var obj struct {
		Type        string          `json:"type"`
		Geometry    json.RawMessage `json:"geometry"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, invalidGeo("invalid JSON: %v", err)
	}
	if obj.Type == "Feature" {
		if len(obj.Geometry) == 0 || string(obj.Geometry) == "null" {
			return nil, invalidGeo("feature has no geometry")
		}
		return ParseGeoJSON(obj.Geometry)
	}

	var lines [][][]float64
	switch obj.Type {
	case "LineString":
		var line [][]float64
		if err := json.Unmarshal(obj.Coordinates, &line); err != nil {
			return nil, invalidGeo("LineString coordinates must be an array of positions")
		}
		lines = [][][]float64{line}
	case "MultiLineString":
		if err := json.Unmarshal(obj.Coordinates, &lines); err != nil {
			return nil, invalidGeo("MultiLineString coordinates must be an array of lines")
		}
	default:
		return nil, invalidGeo("type must be LineString, MultiLineString or a Feature containing one")
	}

	f := &gpx.File{}
	total := 0
	for li, line := range lines {
		if len(line) < 2 {
			return nil, invalidGeo("line %d must have at least 2 positions", li+1)
		}
		total += len(line)
		if total > MaxGeoJSONPoints {
			return nil, invalidGeo("geometry has more than %d points", MaxGeoJSONPoints)
		}
		seg := make(gpx.Segment, len(line))
		for pi, pos := range line {
			if len(pos) < 2 || len(pos) > 3 {
				return nil, invalidGeo("line %d position %d must be [lon, lat] or [lon, lat, elevation]", li+1, pi+1)
			}
			lon, lat := pos[0], pos[1]
			if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
				return nil, invalidGeo("line %d position %d is outside valid coordinates", li+1, pi+1)
			}
			seg[pi] = gpx.Point{Lon: lon, Lat: lat}
			if len(pos) == 3 {
				if math.IsNaN(pos[2]) || math.IsInf(pos[2], 0) {
					return nil, invalidGeo("line %d position %d has invalid elevation", li+1, pi+1)
				}
				seg[pi].Ele, seg[pi].HasEle = pos[2], true
			}
		}
		f.Segments = append(f.Segments, seg)
	}
	if len(f.Segments) == 0 {
		return nil, invalidGeo("geometry has no lines")
	}
	return f, nil
}

func invalidGeo(format string, args ...any) error {
	return apperr.New(apperr.Unprocessable, "invalid GeoJSON: "+fmt.Sprintf(format, args...))
}

func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apperr.New(apperr.Invalid, "name must not be empty")
	}
	if utf8.RuneCountInString(s) > maxNameLength {
		return "", apperr.Newf(apperr.Invalid, "name must be at most %d characters", maxNameLength)
	}
	return s, nil
}

func validateDescription(s string) error {
	if utf8.RuneCountInString(s) > maxDescriptionLength {
		return apperr.Newf(apperr.Invalid, "description must be at most %d characters", maxDescriptionLength)
	}
	return nil
}

func validateVisibility(v string) error {
	if v != VisibilityPrivate && v != VisibilityPublic {
		return apperr.New(apperr.Invalid, "visibility must be private or public")
	}
	return nil
}
