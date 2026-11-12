package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// line is n points heading north from (lat, lon), about 111 m apart.
func line(lat, lon float64, n int) []Point {
	p := make([]Point, n)
	for i := range p {
		p[i] = Point{lat + float64(i)*0.001, lon}
	}
	return p
}

func TestValidate(t *testing.T) {
	r := DefaultRules()
	good := func() Track { return Track{Name: "  Ridge   walk ", Segments: [][]Point{line(46, 8, 20), {}}} }
	tr := good()
	if reason := r.Validate(&tr); reason != "" || tr.Name != "Ridge walk" || len(tr.Segments) != 1 {
		t.Fatalf("good track: %q, %+v", reason, tr.Name)
	}
	cases := map[string]func(*Track){
		"no name":             func(t *Track) { t.Name = " " },
		"needing fixes":       func(t *Track) { t.NeedsFix = true },
		"no geometry":         func(t *Track) { t.Segments = nil },
		"invalid coordinates": func(t *Track) { t.Segments[0][3].Lat = 95 },
		"jump over":           func(t *Track) { t.Segments[0][5].Lat += 1 },
		"fewer than":          func(t *Track) { t.Segments = [][]Point{line(46, 8, 5)} },
		"shorter than": func(t *Track) { // 15 points about 11 m apart
			p := make([]Point, 15)
			for i := range p {
				p[i] = Point{46 + float64(i)*0.0001, 8}
			}
			t.Segments = [][]Point{p}
		},
		"separate pieces": func(t *Track) {
			t.Segments = nil
			for i := range 25 {
				t.Segments = append(t.Segments, line(46+float64(i)*0.1, 8, 12))
			}
		},
		"too fragmented": func(t *Track) { t.Segments = [][]Point{line(46, 8, 12), line(47, 8, 12), line(48, 8, 12)} },
	}
	for want, breakIt := range cases {
		tr := good()
		breakIt(&tr)
		if reason := r.Validate(&tr); !strings.Contains(reason, want) {
			t.Errorf("%s: got %q", want, reason)
		}
	}
	long := Track{Name: "x", Segments: [][]Point{line(0, 8, 20)}}
	r.MaxLengthM = 1000
	if reason := r.Validate(&long); !strings.Contains(reason, "longer than") {
		t.Errorf("max length: %q", reason)
	}
}

func TestOSMJoinsWays(t *testing.T) {
	// Ways in rough order, some drawn backwards, the first one too.
	a, b, c := Point{1, 1}, Point{1, 2}, Point{1, 3}
	d, e := Point{1, 4}, Point{5, 5}
	rel := osmRelation{ID: 7, Tags: map[string]string{"name": "R", "route": "hiking", "network": "lwn", "from": "A", "to": "B"}}
	add := func(role string, pts ...Point) {
		rel.Members = append(rel.Members, struct {
			Type     string  `json:"type"`
			Role     string  `json:"role"`
			Geometry []Point `json:"geometry"`
		}{"way", role, pts})
	}
	add("", b, a)            // backwards first way
	add("", b, c)            // joins at b: the first way gets flipped
	add("", d, c)            // backwards: joins at c
	add("alternative", a, e) // left out
	add("", e, Point{6, 6})  // not connected: a new piece
	tr := rel.track()
	if len(tr.Segments) != 2 {
		t.Fatalf("segments = %v", tr.Segments)
	}
	if got := tr.Segments[0]; len(got) != 4 || got[0] != a || got[3] != d {
		t.Errorf("joined = %v", got)
	}
	if tr.SourceID != "osm:relation:7" || !strings.Contains(tr.Description, "Hiking route (local network) from A to B") ||
		!strings.Contains(tr.Attribution, "OpenStreetMap") || tr.URL != "https://www.openstreetmap.org/relation/7" {
		t.Errorf("track = %+v", tr)
	}
}

func TestOSMFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/overpass.json") // real Overpass output (3 relations near Lauterbrunnen)
	if err != nil {
		t.Fatal(err)
	}
	var res osmResponse
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Elements) != 3 {
		t.Fatalf("%d relations", len(res.Elements))
	}
	for _, rel := range res.Elements {
		tr := rel.track()
		if tr.Name == "" || tr.PointCount() < 2 || len(tr.Segments) == 0 {
			t.Errorf("relation %d: %+v", rel.ID, tr)
		}
		// Way geometries share end points, so they join into few pieces.
		if len(tr.Segments) > 3 {
			t.Errorf("relation %d: %d pieces", rel.ID, len(tr.Segments))
		}
	}
}

func TestGPX(t *testing.T) {
	tr := Track{Name: `Cliffs & "coves" <1>`, Description: "d", URL: "https://example.com/r?a=1&b=2",
		Segments: [][]Point{line(46, 8, 3), line(47, 8, 2)}}
	var doc struct {
		Name string `xml:"trk>name"`
		Segs []struct {
			Pts []struct {
				Lat float64 `xml:"lat,attr"`
			} `xml:"trkpt"`
		} `xml:"trk>trkseg"`
	}
	if err := xml.Unmarshal(tr.GPX(), &doc); err != nil {
		t.Fatalf("invalid XML: %v\n%s", err, tr.GPX())
	}
	if doc.Name != tr.Name || len(doc.Segs) != 2 || len(doc.Segs[0].Pts) != 3 || doc.Segs[1].Pts[0].Lat != 47 {
		t.Errorf("parsed %+v", doc)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := LoadState(path) // missing file: empty state
	if err != nil || len(s.Records) != 0 {
		t.Fatal(s, err)
	}
	s.Records["osm:relation:1"] = &Record{RouteID: "r1", Fingerprint: "f"}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadState(path)
	if err != nil || s2.Records["osm:relation:1"].RouteID != "r1" {
		t.Fatal(s2, err)
	}
}

// fakeAPI is a minimal Travel Docs API: auth, import jobs, route metadata.
type fakeAPI struct {
	mu           sync.Mutex
	registered   bool
	jobs         map[string]string // job ID → route ID
	routes       map[string]string // route ID → description
	versions     map[string]int
	geometryNew  int // new versions created
	busyOnce     bool
	descriptions []string
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !f.registered {
			reply(w, 401, map[string]string{"error": "invalid email or password"})
			return
		}
		reply(w, 200, map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("POST /auth/register", func(w http.ResponseWriter, r *http.Request) {
		f.registered = true
		reply(w, 201, map[string]string{"access_token": "tok"})
	})
	mux.HandleFunc("POST /import-jobs", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Idempotency-Key") == "" {
			reply(w, 400, map[string]string{"error": "auth or key missing"})
			return
		}
		if f.busyOnce {
			f.busyOnce = false
			w.Header().Set("Retry-After", "0")
			reply(w, 429, map[string]string{"error": "you already have 10 imports in progress"})
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			reply(w, 400, map[string]string{"error": "no file"})
			return
		}
		data, _ := io.ReadAll(file)
		if !strings.Contains(string(data), "<trkpt") {
			reply(w, 422, map[string]string{"error": "invalid GPX"})
			return
		}
		routeID := r.FormValue("route_id")
		if routeID != "" {
			f.versions[routeID]++
			f.geometryNew++
		} else {
			routeID = fmt.Sprintf("route-%d", len(f.routes)+1)
			f.routes[routeID] = ""
			f.versions[routeID] = 1
		}
		id := fmt.Sprintf("job-%d", len(f.jobs)+1)
		f.jobs[id] = routeID
		reply(w, 202, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /import-jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, 200, map[string]string{"status": "succeeded", "route_id": f.jobs[r.PathValue("id")]})
	})
	mux.HandleFunc("GET /routes/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, 200, map[string]int{"version": f.versions[r.PathValue("id")]})
	})
	mux.HandleFunc("PATCH /routes/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		if r.Header.Get("If-Match") != fmt.Sprintf(`"%d"`, f.versions[id]) {
			reply(w, 412, map[string]string{"error": "stale"})
			return
		}
		var body struct{ Description string }
		json.NewDecoder(r.Body).Decode(&body)
		f.routes[id] = body.Description
		f.descriptions = append(f.descriptions, body.Description)
		f.versions[id]++
		reply(w, 200, map[string]any{})
	})
	return mux
}

func TestRunExportsNewAndChangedRoutesOnly(t *testing.T) {
	fixture, err := os.ReadFile("testdata/overpass.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	overpassBody := fixture
	overpass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if !strings.Contains(r.Form.Get("data"), "out body geom") || r.Header.Get("User-Agent") == "" {
			http.Error(w, "bad query", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w.Write(overpassBody)
	}))
	defer overpass.Close()
	api := &fakeAPI{jobs: map[string]string{}, routes: map[string]string{}, versions: map[string]int{}, busyOnce: true}
	app := httptest.NewServer(api.handler(t))
	defer app.Close()

	cfg, err := parseFlags([]string{"-api", app.URL, "-overpass", overpass.URL,
		"-state", filepath.Join(t.TempDir(), "state.json"), "-regions", "46.5,7.8,46.6,8.0", "-sources", "osm"})
	if err != nil {
		t.Fatal(err)
	}
	cfg.rules.MinLengthM, cfg.rules.MinPoints = 100, 2 // the fixture's relations are short
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	sum, err := runOnce(ctx, cfg, log)
	if err != nil || sum.Found != 3 || sum.New+len(sum.Rejected) == 0 {
		t.Fatalf("first run: %+v, %v", sum, err)
	}
	first := sum.New
	if first == 0 || len(api.routes) != first || !api.registered {
		t.Fatalf("first run exported %d, api has %d routes", first, len(api.routes))
	}
	for _, d := range api.descriptions {
		if !strings.Contains(d, "OpenStreetMap contributors") || !strings.Contains(d, "openstreetmap.org/relation/") {
			t.Errorf("description without attribution: %q", d)
		}
	}

	// Nothing changed: nothing is sent.
	sum, err = runOnce(ctx, cfg, log)
	if err != nil || sum.New != 0 || sum.Updated != 0 || sum.Unchanged != first {
		t.Fatalf("second run: %+v, %v", sum, err)
	}

	// A relation's geometry changed in OSM: a new version of the same route.
	mu.Lock()
	overpassBody = []byte(strings.Replace(string(fixture), `"lat": 46.5445136`, `"lat": 46.5445999`, 1))
	mu.Unlock()
	sum, err = runOnce(ctx, cfg, log)
	if err != nil || sum.Updated != 1 || sum.New != 0 || api.geometryNew != 1 || len(api.routes) != first {
		t.Fatalf("third run: %+v, %v (new versions %d, routes %d)", sum, err, api.geometryNew, len(api.routes))
	}

	// Dry runs export nothing.
	cfg.dryRun = true
	before := len(api.jobs)
	if _, err := runOnce(ctx, cfg, log); err != nil || len(api.jobs) != before {
		t.Fatalf("dry run sent jobs: %v", err)
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"-regions", "46,7,47,8; 45,6,45.5,6.5", "-routes", "hiking, mtb"})
	if err != nil || len(cfg.regions) != 2 || strings.Join(cfg.routes, ",") != "hiking,mtb" {
		t.Fatalf("%+v %v", cfg, err)
	}
	// Defaults: the whole world, every source.
	cfg, err = parseFlags(nil)
	if err != nil || len(cfg.regions) != len(worldRegions()) || len(cfg.sources) != len(datasets)+1 {
		t.Fatalf("defaults: %d regions, sources %v, %v", len(cfg.regions), cfg.sources, err)
	}
	for _, bad := range [][]string{{"-regions", "47,7,46,8"}, {"-regions", "x"}, {"-routes", `hiking"]`}, {"-visibility", "friends"},
		{"-sources", "osm,strava"}, {"-sources", ""}} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
