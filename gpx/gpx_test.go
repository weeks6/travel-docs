package gpx

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseTrackFile(t *testing.T) {
	f, err := os.Open("testdata/track.gpx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Name != "Morning hike" {
		t.Errorf("Name = %q, want %q", got.Name, "Morning hike")
	}
	if got.Description != "A short loop" {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(got.Segments))
	}
	if got.PointCount() != 5 {
		t.Errorf("PointCount = %d, want 5", got.PointCount())
	}
	p := got.Segments[0][0]
	if p.Lat != 46.5 || p.Lon != 8.0 || !p.HasEle || p.Ele != 1000 {
		t.Errorf("first point = %+v", p)
	}
	start, ok := got.StartTime()
	if !ok || !start.Equal(time.Date(2026, 6, 1, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("StartTime = %v, %v", start, ok)
	}
}

func TestParseGPX10RouteWithoutNamespaceOrElevation(t *testing.T) {
	doc := `<?xml version="1.0"?>
<gpx version="1.0" creator="test">
  <name>Old format</name>
  <rte>
    <rtept lat="10" lon="20"/>
    <rtept lat="10.1" lon="20.1"><time>not a time</time></rtept>
  </rte>
</gpx>`
	got, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Name != "Old format" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Segments) != 1 || len(got.Segments[0]) != 2 {
		t.Fatalf("segments = %+v", got.Segments)
	}
	if got.Segments[0][0].HasEle {
		t.Error("expected no elevation")
	}
	if _, ok := got.StartTime(); ok {
		t.Error("expected no start time; invalid timestamps should be ignored")
	}
}

func TestParseDropsShortSegments(t *testing.T) {
	doc := `<gpx xmlns="http://www.topografix.com/GPX/1/1"><trk>
		<trkseg><trkpt lat="1" lon="1"/></trkseg>
		<trkseg><trkpt lat="1" lon="1"/><trkpt lat="2" lon="2"/></trkseg>
	</trk></gpx>`
	got, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Segments) != 1 {
		t.Errorf("got %d segments, want 1", len(got.Segments))
	}
}

func TestParseInvalid(t *testing.T) {
	tests := map[string]struct {
		doc     string
		wantMsg string
	}{
		"empty":         {"", "no XML document"},
		"not xml":       {"hello world", "no XML document"},
		"wrong root":    {`<kml><Document/></kml>`, "root element must be <gpx>"},
		"truncated":     {`<gpx><trk><trkseg><trkpt lat="1" lon="1">`, "malformed XML"},
		"no points":     {`<gpx><trk><trkseg></trkseg></trk></gpx>`, "at least 2 points"},
		"single point":  {`<gpx><trk><trkseg><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, "at least 2 points"},
		"lat range":     {`<gpx><trk><trkseg><trkpt lat="91" lon="1"/><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, "lat 91 out of range"},
		"lon range":     {`<gpx><trk><trkseg><trkpt lat="1" lon="-181"/><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, "lon -181 out of range"},
		"missing lon":   {`<gpx><trk><trkseg><trkpt lat="1"/><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, "missing lon"},
		"bad lat":       {`<gpx><trk><trkseg><trkpt lat="abc" lon="1"/><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, `invalid lat "abc"`},
		"nan lat":       {`<gpx><trk><trkseg><trkpt lat="NaN" lon="1"/><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, `invalid lat`},
		"bad elevation": {`<gpx><trk><trkseg><trkpt lat="1" lon="1"><ele>high</ele></trkpt><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, "invalid elevation"},
		"bad route pt":  {`<gpx><rte><rtept lat="1" lon="500"/><rtept lat="1" lon="1"/></rte></gpx>`, "route 1"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.doc))
			if err == nil {
				t.Fatal("expected error")
			}
			if !IsValidationError(err) {
				t.Errorf("expected ValidationError, got %T", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tc.wantMsg)
			}
		})
	}
}
