// Package gpx parses and validates GPX 1.0/1.1 files.
package gpx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// Point is a single track/route point.
type Point struct {
	Lat    float64
	Lon    float64
	Ele    float64
	HasEle bool
	Time   time.Time // zero when the point has no timestamp
}

// Segment is a continuous run of points. Distance is not counted across
// segment boundaries (e.g. pauses or separate tracks).
type Segment []Point

// File is a parsed and validated GPX document.
type File struct {
	Name        string
	Description string
	Segments    []Segment
}

// ValidationError means the input is not a usable GPX file.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return "invalid GPX: " + e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// IsValidationError reports whether err is a *ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

type xmlGPX struct {
	XMLName  xml.Name `xml:"gpx"`
	Name     string   `xml:"name"` // GPX 1.0 keeps name at the root
	Desc     string   `xml:"desc"`
	Metadata struct {
		Name string `xml:"name"`
		Desc string `xml:"desc"`
	} `xml:"metadata"`
	Tracks []xmlTrack `xml:"trk"`
	Routes []xmlRoute `xml:"rte"`
}

type xmlTrack struct {
	Name     string `xml:"name"`
	Desc     string `xml:"desc"`
	Segments []struct {
		Points []xmlPoint `xml:"trkpt"`
	} `xml:"trkseg"`
}

type xmlRoute struct {
	Name   string     `xml:"name"`
	Desc   string     `xml:"desc"`
	Points []xmlPoint `xml:"rtept"`
}

type xmlPoint struct {
	Lat  *string `xml:"lat,attr"`
	Lon  *string `xml:"lon,attr"`
	Ele  *string `xml:"ele"`
	Time *string `xml:"time"`
}

// Parse reads and validates a GPX document. Segments with fewer than two
// points are dropped; the file must contain at least one usable segment.
// Callers should limit the size of r.
func Parse(r io.Reader) (*File, error) {
	var doc xmlGPX
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, invalid("no XML document found")
		}
		var ue xml.UnmarshalError
		if errors.As(err, &ue) && strings.HasPrefix(string(ue), "expected element type <gpx>") {
			return nil, invalid("root element must be <gpx>")
		}
		return nil, invalid("malformed XML: %v", err)
	}

	f := &File{
		Name:        firstNonEmpty(doc.Metadata.Name, doc.Name),
		Description: firstNonEmpty(doc.Metadata.Desc, doc.Desc),
	}

	for ti, trk := range doc.Tracks {
		f.Name = firstNonEmpty(f.Name, trk.Name)
		f.Description = firstNonEmpty(f.Description, trk.Desc)
		for si, seg := range trk.Segments {
			pts, err := convertPoints(seg.Points)
			if err != nil {
				return nil, invalid("track %d, segment %d: %v", ti+1, si+1, err)
			}
			if len(pts) >= 2 {
				f.Segments = append(f.Segments, pts)
			}
		}
	}

	for ri, rte := range doc.Routes {
		f.Name = firstNonEmpty(f.Name, rte.Name)
		f.Description = firstNonEmpty(f.Description, rte.Desc)
		pts, err := convertPoints(rte.Points)
		if err != nil {
			return nil, invalid("route %d: %v", ri+1, err)
		}
		if len(pts) >= 2 {
			f.Segments = append(f.Segments, pts)
		}
	}

	if len(f.Segments) == 0 {
		return nil, invalid("no track or route with at least 2 points")
	}
	return f, nil
}

func convertPoints(in []xmlPoint) (Segment, error) {
	out := make(Segment, 0, len(in))
	for i, p := range in {
		lat, err := parseCoord(p.Lat, "lat", -90, 90)
		if err != nil {
			return nil, fmt.Errorf("point %d: %w", i+1, err)
		}
		lon, err := parseCoord(p.Lon, "lon", -180, 180)
		if err != nil {
			return nil, fmt.Errorf("point %d: %w", i+1, err)
		}
		pt := Point{Lat: lat, Lon: lon}

		if p.Ele != nil && strings.TrimSpace(*p.Ele) != "" {
			ele, err := strconv.ParseFloat(strings.TrimSpace(*p.Ele), 64)
			if err != nil || math.IsNaN(ele) || math.IsInf(ele, 0) {
				return nil, fmt.Errorf("point %d: invalid elevation %q", i+1, *p.Ele)
			}
			pt.Ele, pt.HasEle = ele, true
		}

		// Timestamps are optional and devices are sloppy with them, so a bad
		// timestamp is ignored rather than rejecting the whole file.
		if p.Time != nil {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(*p.Time)); err == nil {
				pt.Time = t
			}
		}
		out = append(out, pt)
	}
	return out, nil
}

func parseCoord(s *string, name string, min, max float64) (float64, error) {
	if s == nil {
		return 0, fmt.Errorf("missing %s attribute", name)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil || math.IsNaN(v) {
		return 0, fmt.Errorf("invalid %s %q", name, *s)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%s %v out of range [%v, %v]", name, v, min, max)
	}
	return v, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// PointCount returns the total number of points across all segments.
func (f *File) PointCount() int {
	n := 0
	for _, s := range f.Segments {
		n += len(s)
	}
	return n
}

// StartTime returns the first timestamp in the file, if any.
func (f *File) StartTime() (time.Time, bool) {
	for _, s := range f.Segments {
		for _, p := range s {
			if !p.Time.IsZero() {
				return p.Time, true
			}
		}
	}
	return time.Time{}, false
}

// FillElevation fills in missing elevations by linear interpolation between
// the nearest known points (copying the nearest value at the ends), treating
// all segments as one sequence. It reports whether the file has any
// elevation data; if not, nothing is changed.
func (f *File) FillElevation() bool {
	type ref struct{ seg, pt int }
	var all []ref
	for si, s := range f.Segments {
		for pi := range s {
			all = append(all, ref{si, pi})
		}
	}
	at := func(i int) *Point { return &f.Segments[all[i].seg][all[i].pt] }

	prev := -1 // index of the last point with elevation
	for i := range all {
		if !at(i).HasEle {
			continue
		}
		start := prev + 1
		for j := start; j < i; j++ {
			p := at(j)
			if prev < 0 {
				p.Ele = at(i).Ele
			} else {
				t := float64(j-prev) / float64(i-prev)
				p.Ele = at(prev).Ele + t*(at(i).Ele-at(prev).Ele)
			}
			p.HasEle = true
		}
		prev = i
	}
	if prev < 0 {
		return false
	}
	for j := prev + 1; j < len(all); j++ {
		p := at(j)
		p.Ele, p.HasEle = at(prev).Ele, true
	}
	return true
}
