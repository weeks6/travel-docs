package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseRegions(t *testing.T) {
	world, err := ParseRegions("world")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, rs := range regionGroups {
		total += len(rs)
	}
	if len(world) != total {
		t.Errorf("world has %d regions, want %d", len(world), total)
	}
	// Continents take turns, so a few regions already span the world.
	firsts := map[string]bool{}
	for _, r := range world[:len(groupOrder)] {
		for g, rs := range regionGroups {
			for _, x := range rs {
				if x.Name == r.Name {
					firsts[g] = true
				}
			}
		}
	}
	if len(firsts) != len(groupOrder) {
		t.Errorf("first regions come from %v", firsts)
	}
	for _, rs := range regionGroups {
		for _, r := range rs {
			if r.South >= r.North || r.West >= r.East || r.North-r.South > 1 || r.East-r.West > 1.6 {
				t.Errorf("region %s: bad or too large box %+v", r.Name, r.BBox)
			}
		}
	}

	got, err := ParseRegions("yosemite; asia; 1,2,3,4; yosemite")
	if err != nil || got[0].Name != "yosemite" || len(got) != 1+len(regionGroups["asia"])+1 || got[len(got)-1].South != 1 {
		t.Errorf("mixed = %v, %v", got, err)
	}
	for _, bad := range []string{"atlantis", "", "3,2,1,0"} {
		if _, err := ParseRegions(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestJoinSegments(t *testing.T) {
	a, b, c, d := Point{0, 0}, Point{0, 0.01}, Point{0, 0.02}, Point{0, 0.03}
	near := Point{0.00005, 0.02} // ~6 m from c
	far := Point{1, 1}
	got := joinSegments([][]Point{
		{c, b},          // backwards, in the middle
		{a, b},          // the start
		{near, d},       // joins c within the tolerance
		{far, {1, 1.1}}, // on its own
		{a},             // a single point: dropped
	}, 15)
	if len(got) != 2 {
		t.Fatalf("got %d lines: %v", len(got), got)
	}
	line := got[0]
	if len(got[1]) > len(line) {
		line = got[1] // the far piece is longer, so it may come first
	}
	ends := [2]Point{line[0], line[len(line)-1]}
	if ends != [2]Point{a, d} && ends != [2]Point{d, a} || len(line) != 4 { // c and near are one point
		t.Errorf("joined = %v", line)
	}
}

// fakeArcGIS serves one layer: a trail in two segments, given in reverse
// order, and a second trail; pages hold 2 features.
func fakeArcGIS(t *testing.T) (*httptest.Server, *[]url.Values) {
	var mu sync.Mutex
	var queries []url.Values
	type feat struct {
		attrs map[string]any
		path  [][2]float64
	}
	feats := []feat{
		{map[string]any{"OBJECTID": 1, "PARK": "YOSE", "NAME": "Mist Trail", "UNIT": "Yosemite"}, [][2]float64{{-119.52, 37.73}, {-119.51, 37.735}, {-119.50, 37.74}}},
		{map[string]any{"OBJECTID": 2, "PARK": "YOSE", "NAME": "Mist Trail", "UNIT": "Yosemite"}, [][2]float64{{-119.54, 37.72}, {-119.53, 37.725}, {-119.52, 37.73}}},
		{map[string]any{"OBJECTID": 3, "PARK": "YOSE", "NAME": "Half Dome's Cable", "UNIT": "Yosemite"}, [][2]float64{{-119.53, 37.74}, {-119.52, 37.75}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		queries = append(queries, r.PostForm)
		mu.Unlock()
		if r.URL.Path != "/layer/query" || r.PostForm.Get("f") != "json" || r.Header.Get("User-Agent") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		where := r.PostForm.Get("where")
		var match []feat
		for _, f := range feats {
			// The geometry query names its routes; the first one only filters.
			if strings.Contains(where, "NAME") && !strings.Contains(where, "'"+strings.ReplaceAll(f.attrs["NAME"].(string), "'", "''")+"'") {
				continue
			}
			match = append(match, f)
		}
		var offset int
		fmt.Sscan(r.PostForm.Get("resultOffset"), &offset)
		page := match[min(offset, len(match)):min(offset+2, len(match))]
		var out []map[string]any
		for _, f := range page {
			o := map[string]any{"attributes": f.attrs}
			if r.PostForm.Get("returnGeometry") == "true" {
				o["geometry"] = map[string]any{"paths": [][][2]float64{f.path}}
			}
			out = append(out, o)
		}
		json.NewEncoder(w).Encode(map[string]any{"features": out, "exceededTransferLimit": offset+2 < len(match)})
	}))
	return srv, &queries
}

func TestArcGISSource(t *testing.T) {
	srv, queries := fakeArcGIS(t)
	defer srv.Close()
	src := &ArcGISSource{UserAgent: "test", HTTP: srv.Client(), Dataset: ArcGISDataset{
		ID: "parks", Layer: srv.URL + "/layer", Coverage: []BBox{{30, -125, 45, -110}},
		Where: "STATUS = 'open'", OIDField: "OBJECTID", KeyFields: []string{"PARK", "NAME"}, Fields: []string{"UNIT"},
		Attribution: "Public domain.",
		Describe: func(a attrs, t *Track) {
			t.Name, t.Description, t.URL = a.String("NAME"), "In "+a.String("UNIT")+".", "https://example.com/"+a.String("PARK")
		},
	}}
	ctx := context.Background()

	// A region the dataset doesn't cover: no requests.
	if tracks, err := src.Tracks(ctx, Region{Name: "alps", BBox: BBox{46, 7, 47, 8}}); err != nil || len(tracks) != 0 || len(*queries) != 0 {
		t.Fatalf("outside coverage: %v, %v, %d queries", tracks, err, len(*queries))
	}

	tracks, err := src.Tracks(ctx, Region{Name: "yosemite", BBox: BBox{37.5, -119.8, 38.0, -119.2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks: %+v", len(tracks), tracks)
	}
	var mist Track
	for _, tr := range tracks {
		if tr.Name == "Mist Trail" {
			mist = tr
		}
	}
	// Both segments, joined into one line in order, described and attributed.
	if mist.SourceID != "parks:YOSE|Mist Trail" || len(mist.Segments) != 1 || len(mist.Segments[0]) != 5 ||
		mist.Description != "In Yosemite." || mist.URL != "https://example.com/YOSE" || mist.Attribution != "Public domain." {
		t.Errorf("mist = %+v", mist)
	}

	// The first query finds routes in the region, attributes only; the next
	// fetch their geometry by key (quotes escaped), all pages of each.
	q := *queries
	first := q[0]
	if first.Get("returnGeometry") != "false" || first.Get("geometryType") != "esriGeometryEnvelope" ||
		first.Get("geometry") != "-119.800000,37.500000,-119.200000,38.000000" || first.Get("where") != "STATUS = 'open'" {
		t.Errorf("first query = %v", first)
	}
	var geom []url.Values
	for _, v := range q {
		if v.Get("returnGeometry") == "true" {
			geom = append(geom, v)
		}
	}
	// Pages of 2 here: the first query took two pages; the geometry query
	// for 3 features too.
	if len(q) != 4 || q[1].Get("resultOffset") != "2" || len(geom) != 2 || geom[1].Get("resultOffset") != "2" {
		t.Errorf("queries: %d, offsets %q %q", len(q), q[1].Get("resultOffset"), geom[len(geom)-1].Get("resultOffset"))
	}
	w := geom[0].Get("where")
	if !strings.HasPrefix(w, "(STATUS = 'open') AND (") || !strings.Contains(w, "NAME = 'Half Dome''s Cable'") ||
		geom[0].Get("outSR") != "4326" || geom[0].Get("geometry") != "" {
		t.Errorf("geometry query = %v", geom[0])
	}
}

func TestDuplicateOf(t *testing.T) {
	s := &State{Records: map[string]*Record{}}
	official := Track{SourceID: "nps:YOSE|Mist Trail", Segments: [][]Point{line(37.7, -119.5, 30)}}
	start, end := official.Ends()
	s.Records[official.SourceID] = &Record{RouteID: "r1", Start: &start, End: &end, LengthM: official.LengthM()}

	osm := Track{SourceID: "osm:relation:9", Segments: [][]Point{line(37.7003, -119.5, 30)}} // ~30 m north
	if got := s.DuplicateOf(osm); got != official.SourceID {
		t.Errorf("same trail: %q", got)
	}
	rev := osm
	rev.Segments = [][]Point{append([]Point(nil), osm.Segments[0]...)}
	reverse(rev.Segments[0])
	if got := s.DuplicateOf(rev); got != official.SourceID {
		t.Errorf("same trail reversed: %q", got)
	}
	for name, tr := range map[string]Track{
		"same source":    {SourceID: "nps:YOSE|Other", Segments: osm.Segments},
		"shorter":        {SourceID: "osm:relation:10", Segments: [][]Point{line(37.7, -119.5, 15)}},
		"somewhere else": {SourceID: "osm:relation:11", Segments: [][]Point{line(38.7, -119.5, 30)}},
	} {
		if got := s.DuplicateOf(tr); got != "" {
			t.Errorf("%s: duplicate of %q", name, got)
		}
	}
}

// TestRunTakesTurnsOverRegions checks the rotation: each run scans the next
// regions, an export limit makes the next run resume in the same region,
// and official sources win over OSM for the same trail.
func TestRunTakesTurnsOverRegions(t *testing.T) {
	var mu sync.Mutex
	var scanned []string
	// Overpass: in region "b", a relation that duplicates the ArcGIS trail;
	// elsewhere, two relations per region.
	overpass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		q := r.Form.Get("data")
		var south float64
		fmt.Sscanf(q[strings.Index(q, `["name"](`)+9:], "%f", &south)
		mu.Lock()
		scanned = append(scanned, fmt.Sprint(south))
		mu.Unlock()
		rel := func(id int64, lat, lon float64) map[string]any {
			var geom []map[string]float64
			for _, p := range line(lat, lon, 30) {
				geom = append(geom, map[string]float64{"lat": p.Lat, "lon": p.Lon})
			}
			return map[string]any{"type": "relation", "id": id, "tags": map[string]string{"name": fmt.Sprint("R", id), "route": "hiking"},
				"members": []any{map[string]any{"type": "way", "role": "", "geometry": geom}}}
		}
		base := int64(south * 100)
		els := []any{rel(base+1, south+0.1, 0.1), rel(base+2, south+0.2, 0.2)}
		if south == 10 { // region b: the same trail as the ArcGIS one
			els = []any{rel(base+1, 10.5, 0.5)}
		}
		json.NewEncoder(w).Encode(map[string]any{"elements": els})
	}))
	defer overpass.Close()
	arcgis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		path := [][]float64{}
		for _, p := range line(10.5, 0.5, 30) {
			path = append(path, []float64{p.Lon, p.Lat})
		}
		f := map[string]any{"attributes": map[string]any{"ID": 1, "NAME": "Official"}}
		if r.PostForm.Get("returnGeometry") == "true" {
			f["geometry"] = map[string]any{"paths": [][][]float64{path}}
		}
		json.NewEncoder(w).Encode(map[string]any{"features": []any{f}})
	}))
	defer arcgis.Close()
	api := &fakeAPI{jobs: map[string]string{}, routes: map[string]string{}, versions: map[string]int{}}
	app := httptest.NewServer(api.handler(t))
	defer app.Close()

	official := ArcGISDataset{ID: "official", Layer: arcgis.URL, Coverage: []BBox{{10, 0, 11, 1}}, Where: "1=1", OIDField: "ID",
		KeyFields: []string{"ID"}, Fields: []string{"NAME"}, Attribution: "PD",
		Describe: func(a attrs, t *Track) { t.Name = a.String("NAME") }}
	datasets = append(datasets, official)
	defer func() { datasets = datasets[:len(datasets)-1] }()

	cfg, err := parseFlags([]string{"-api", app.URL, "-overpass", overpass.URL, "-state", filepath.Join(t.TempDir(), "state.json"),
		"-regions", "0,0,1,1; 10,0,11,1; 20,0,21,1", "-regions-per-run", "2", "-sources", "osm,official"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	run := func() summary {
		t.Helper()
		mu.Lock()
		scanned = nil
		mu.Unlock()
		sum, err := runOnce(ctx, cfg, log)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}

	// Run 1: regions a and b. In b, the official trail is exported and
	// OSM's copy of it is skipped.
	sum := run()
	if strings.Join(sum.Regions, ",") != "0,0,1,1,10,0,11,1" || sum.New != 3 ||
		sum.Rejected["same trail as another source's route"] != 1 {
		t.Fatalf("run 1: %+v", sum)
	}
	if _, ok := api.routes["route-3"]; !ok {
		t.Fatalf("routes: %v", api.routes)
	}
	// Run 2: region c, then round to a again (unchanged).
	sum = run()
	if strings.Join(sum.Regions, ",") != "20,0,21,1,0,0,1,1" || sum.New != 2 || sum.Unchanged != 2 {
		t.Fatalf("run 2: %+v", sum)
	}
	// Run 3 with a limit of 1 export, after region b's OSM relations change:
	// it stops in b, and the next run resumes there.
	cfg.limit = 1
	cfg.regions = append(cfg.regions, Region{Name: "d", BBox: BBox{30, 0, 31, 1}})
	sum = run() // regions b, c; b has nothing new; c unchanged; then...
	if strings.Join(sum.Regions, ",") != "10,0,11,1,20,0,21,1" {
		t.Fatalf("run 3: %+v", sum)
	}
	sum = run() // region d: two new routes, limit 1: stops there
	if sum.New != 1 || strings.Join(sum.Regions, ",") != "d" {
		t.Fatalf("run 4: %+v", sum)
	}
	sum = run() // resumes in d
	if sum.New != 1 || sum.Regions[0] != "d" {
		t.Fatalf("run 5: %+v", sum)
	}
}
