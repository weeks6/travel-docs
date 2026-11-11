package routes

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/gpx"
)

func TestParseListParams(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	const coll = "01900000-0000-7000-8000-000000000001"
	tests := []struct {
		query    string
		loggedIn bool
		want     ListParams
		wantErr  string
	}{
		{"", false, ListParams{Page: 1, PageSize: 20, Scope: ScopePublic, Sort: SortCreatedAt, Desc: true}, ""},
		{"", true, ListParams{Page: 1, PageSize: 20, Scope: ScopeMine, Sort: SortCreatedAt, Desc: true}, ""},
		{"collection_id=" + coll, true, ListParams{Page: 1, PageSize: 20, Scope: ScopeAll, CollectionID: coll, Sort: SortCreatedAt, Desc: true}, ""},
		{
			"page=3&page_size=50&scope=shared&name=%20alps%20&min_distance_m=1000&max_distance_m=5000.5&min_ascent_m=10&max_ascent_m=900&bbox=5,45,10,48&sort=distance&order=asc",
			true,
			ListParams{
				Page: 3, PageSize: 50, Scope: ScopeShared, Name: "alps",
				MinDistanceM: f(1000), MaxDistanceM: f(5000.5), MinAscentM: f(10), MaxAscentM: f(900),
				BBox: &BBox{MinLon: 5, MinLat: 45, MaxLon: 10, MaxLat: 48},
				Sort: SortDistance, Desc: false,
			}, "",
		},
		{"page=0", true, ListParams{}, "page must be"},
		{"page=abc", true, ListParams{}, "page must be"},
		{"page_size=101", true, ListParams{}, "page_size must be"},
		{"min_distance_m=-1", true, ListParams{}, "min_distance_m must be"},
		{"max_ascent_m=x", true, ListParams{}, "max_ascent_m must be"},
		{"bbox=1,2,3", true, ListParams{}, "bbox must be"},
		{"bbox=10,45,5,48", true, ListParams{}, "bbox must be"},
		{"bbox=0,0,200,1", true, ListParams{}, "bbox must be"},
		{"sort=id", true, ListParams{}, "sort must be"},
		{"order=up", true, ListParams{}, "order must be"},
		{"scope=everything", true, ListParams{}, "scope must be"},
		{"collection_id=abc", true, ListParams{}, "collection_id must be"},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseListParams(q, tc.loggedIn)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || apperr.KindOf(err) != apperr.Invalid {
					t.Fatalf("err = %v, want Invalid %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("got  %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestParseGeoJSON(t *testing.T) {
	f, err := ParseGeoJSON([]byte(`{"type":"MultiLineString","coordinates":[[[8,46,100],[8.1,46.1]],[[9,47],[9.1,47.1]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Segments) != 2 || !f.Segments[0][0].HasEle || f.Segments[0][1].HasEle || f.Segments[1][1].Lat != 47.1 {
		t.Errorf("segments = %+v", f.Segments)
	}

	f, err = ParseGeoJSON([]byte(`{"type":"Feature","geometry":{"type":"LineString","coordinates":[[1,2],[3,4]]}}`))
	if err != nil || len(f.Segments) != 1 || f.Segments[0][1].Lon != 3 {
		t.Errorf("feature: %v %+v", err, f)
	}

	for _, bad := range []string{
		`[]`, `{}`, `{"type":"Point","coordinates":[1,2]}`, `{"type":"Feature","geometry":null}`,
		`{"type":"LineString","coordinates":[[1,2]]}`, `{"type":"LineString","coordinates":[[1],[2,3]]}`,
		`{"type":"LineString","coordinates":[[181,0],[0,0]]}`, `{"type":"LineString","coordinates":"x"}`,
		`{"type":"MultiLineString","coordinates":[]}`,
	} {
		if _, err := ParseGeoJSON([]byte(bad)); apperr.KindOf(err) != apperr.Unprocessable {
			t.Errorf("%s: err = %v, want Unprocessable", bad, err)
		}
	}
}

func TestBuildContent(t *testing.T) {
	f, err := gpx.Parse(strings.NewReader(`<gpx><metadata><name>From file</name></metadata><trk><trkseg>
		<trkpt lat="46" lon="8"><ele>100</ele><time>2026-05-01T10:00:00+02:00</time></trkpt>
		<trkpt lat="46.01" lon="8"/>
		<trkpt lat="46.02" lon="8"><ele>120</ele></trkpt>
	</trkseg></trk></gpx>`))
	if err != nil {
		t.Fatal(err)
	}
	c, err := BuildContent(f, "/tmp/../dir/route.gpx", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "From file" || c.OriginalFilename != "route.gpx" || !c.HasElevation || c.PointCount != 3 {
		t.Errorf("content = %+v", c)
	}
	if c.StartedAt == nil || c.StartedAt.Location().String() != "UTC" || c.StartedAt.Hour() != 8 {
		t.Errorf("started_at = %v", c.StartedAt)
	}
	var geom struct {
		Type        string         `json:"type"`
		Coordinates [][][3]float64 `json:"coordinates"`
	}
	json.Unmarshal(c.Geometry, &geom)
	if geom.Type != "MultiLineString" || geom.Coordinates[0][1] != [3]float64{8, 46.01, 110} {
		t.Errorf("geometry = %s", c.Geometry)
	}

	long := strings.Repeat("é", 300)
	c, _ = BuildContent(f, "x.gpx", long)
	if len([]rune(c.Name)) != maxNameLength {
		t.Errorf("name not truncated: %d runes", len([]rune(c.Name)))
	}
}

func TestParseIfMatch(t *testing.T) {
	for header, want := range map[string]int{"": 0, "*": 0, `"3"`: 3, `W/"12"`: 12, "7": 7} {
		got, err := ParseIfMatch(header)
		if err != nil || got != want {
			t.Errorf("If-Match %q = %d, %v; want %d", header, got, err, want)
		}
	}
	for _, bad := range []string{`"abc"`, `"0"`, `"-1"`} {
		if _, err := ParseIfMatch(bad); apperr.KindOf(err) != apperr.Invalid {
			t.Errorf("If-Match %q: err = %v, want Invalid", bad, err)
		}
	}
}
