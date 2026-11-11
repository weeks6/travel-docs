package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/weeks6/travel-docs/routestats"
)

const sampleGPX = `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><name>Ridge walk</name><trkseg>
    <trkpt lat="46.0" lon="8.0"><ele>1000</ele><time>2026-05-01T08:00:00Z</time></trkpt>
    <trkpt lat="46.01" lon="8.0"><ele>1100</ele></trkpt>
    <trkpt lat="46.02" lon="8.0"><ele>1050</ele></trkpt>
  </trkseg></trk>
</gpx>`

// memStore is a minimal in-memory Store for handler tests.
type memStore struct {
	mu      sync.Mutex
	routes  map[string]NewRoute
	listArg ListParams
}

func newMemStore() *memStore { return &memStore{routes: map[string]NewRoute{}} }

func (m *memStore) Create(_ context.Context, r NewRoute) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[r.ID] = r
	return r.Route, nil
}

func (m *memStore) Get(_ context.Context, id string) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[id]
	if !ok {
		return Route{}, ErrNotFound
	}
	return r.Route, nil
}

func (m *memStore) List(_ context.Context, p ListParams) (ListResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listArg = p
	res := ListResult{Page: p.Page, PageSize: p.PageSize, Items: []Route{}}
	for _, r := range m.routes {
		res.Items = append(res.Items, r.Route)
	}
	res.Total = len(res.Items)
	return res, nil
}

func (m *memStore) Geometry(_ context.Context, id string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return r.Geometry, nil
}

func (m *memStore) ElevationProfile(_ context.Context, id string) ([]routestats.ProfilePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return r.Profile, nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.routes[id]; !ok {
		return ErrNotFound
	}
	delete(m.routes, id)
	return nil
}

func newTestServer(t *testing.T, store Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	(&Handler{Store: store}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// multipartBody builds an upload request body. Empty filename skips the file field.
func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(content)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func upload(t *testing.T, srv *httptest.Server, filename string, content []byte, fields map[string]string) *http.Response {
	t.Helper()
	body, ct := multipartBody(t, filename, content, fields)
	resp, err := http.Post(srv.URL+"/routes", ct, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return v
}

func TestUploadCreatesRoute(t *testing.T) {
	store := newMemStore()
	srv := newTestServer(t, store)

	resp := upload(t, srv, "ridge.gpx", []byte(sampleGPX), nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	r := decode[Route](t, resp)
	if r.Name != "Ridge walk" || r.OriginalFilename != "ridge.gpx" || r.PointCount != 3 {
		t.Errorf("route = %+v", r)
	}
	if r.AscentM != 100 || r.DescentM != 50 {
		t.Errorf("ascent/descent = %v/%v, want 100/50", r.AscentM, r.DescentM)
	}
	if r.DistanceM < 2200 || r.DistanceM > 2250 { // 0.02° latitude ≈ 2224 m
		t.Errorf("distance = %v", r.DistanceM)
	}
	if r.StartedAt == nil || r.StartedAt.Year() != 2026 {
		t.Errorf("started_at = %v", r.StartedAt)
	}
	if loc := resp.Header.Get("Location"); loc != "/routes/"+r.ID {
		t.Errorf("Location = %q", loc)
	}

	stored := store.routes[r.ID]
	var geom struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(stored.Geometry, &geom); err != nil {
		t.Fatal(err)
	}
	if geom.Type != "MultiLineString" || len(geom.Coordinates) != 1 || geom.Coordinates[0][0] != [2]float64{8, 46} {
		t.Errorf("geometry = %s", stored.Geometry)
	}
}

func TestUploadNameFallbacks(t *testing.T) {
	noName := `<gpx><trk><trkseg><trkpt lat="1" lon="1"/><trkpt lat="1.1" lon="1"/></trkseg></trk></gpx>`
	tests := []struct {
		desc, filename, content, override, want string
	}{
		{"override wins", "a.gpx", sampleGPX, "  My name ", "My name"},
		{"name from file", "a.gpx", sampleGPX, "", "Ridge walk"},
		{"name from filename", "dir/evening-run.gpx", noName, "", "evening-run"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			srv := newTestServer(t, newMemStore())
			resp := upload(t, srv, tc.filename, []byte(tc.content), map[string]string{"name": tc.override})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := decode[Route](t, resp).Name; got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUploadErrors(t *testing.T) {
	srv := newTestServer(t, newMemStore())

	t.Run("invalid gpx", func(t *testing.T) {
		resp := upload(t, srv, "bad.gpx", []byte("<gpx><trk/></gpx>"), nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", resp.StatusCode)
		}
		if msg := decode[map[string]string](t, resp)["error"]; !strings.Contains(msg, "invalid GPX") {
			t.Errorf("error = %q", msg)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		resp := upload(t, srv, "", nil, map[string]string{"name": "x"})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("not multipart", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/routes", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("too large", func(t *testing.T) {
		big := bytes.Repeat([]byte(" "), MaxUploadBytes+1)
		resp := upload(t, srv, "big.gpx", big, nil)
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", resp.StatusCode)
		}
	})
}

func TestGetGeometryElevationDelete(t *testing.T) {
	srv := newTestServer(t, newMemStore())
	r := decode[Route](t, upload(t, srv, "ridge.gpx", []byte(sampleGPX), nil))

	get := func(path string) *http.Response {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if resp := get("/routes/" + r.ID); resp.StatusCode != http.StatusOK || decode[Route](t, resp).ID != r.ID {
		t.Errorf("GET route: status %d", resp.StatusCode)
	}

	resp := get("/routes/" + r.ID + "/geometry")
	if ct := resp.Header.Get("Content-Type"); ct != "application/geo+json" {
		t.Errorf("geometry content type = %q", ct)
	}
	feature := decode[map[string]any](t, resp)
	if feature["type"] != "Feature" || feature["geometry"].(map[string]any)["type"] != "MultiLineString" {
		t.Errorf("feature = %v", feature)
	}

	elev := decode[struct {
		Points []routestats.ProfilePoint `json:"points"`
	}](t, get("/routes/"+r.ID+"/elevation"))
	if len(elev.Points) != 3 || elev.Points[1].ElevationM != 1100 {
		t.Errorf("elevation = %+v", elev.Points)
	}

	del := func(id string) int {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes/"+id, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := del(r.ID); code != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", code)
	}
	if code := del(r.ID); code != http.StatusNotFound {
		t.Errorf("second DELETE status = %d, want 404", code)
	}
	if resp := get("/routes/" + r.ID); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", resp.StatusCode)
	}
	if resp := get("/routes/not-a-uuid"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET malformed id = %d, want 404", resp.StatusCode)
	}
}

func TestParseListParams(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	tests := []struct {
		query   string
		want    ListParams
		wantErr string
	}{
		{"", ListParams{Page: 1, PageSize: 20, Sort: SortCreatedAt, Desc: true}, ""},
		{
			"page=3&page_size=50&name=%20alps%20&min_distance_m=1000&max_distance_m=5000.5&min_ascent_m=10&max_ascent_m=900&bbox=5,45,10,48&sort=distance&order=asc",
			ListParams{
				Page: 3, PageSize: 50, Name: "alps",
				MinDistanceM: f(1000), MaxDistanceM: f(5000.5), MinAscentM: f(10), MaxAscentM: f(900),
				BBox: &BBox{MinLon: 5, MinLat: 45, MaxLon: 10, MaxLat: 48},
				Sort: SortDistance, Desc: false,
			}, "",
		},
		{"page=0", ListParams{}, "page must be"},
		{"page=abc", ListParams{}, "page must be"},
		{"page_size=101", ListParams{}, "page_size must be"},
		{"min_distance_m=-1", ListParams{}, "min_distance_m must be"},
		{"max_ascent_m=x", ListParams{}, "max_ascent_m must be"},
		{"bbox=1,2,3", ListParams{}, "bbox must be"},
		{"bbox=10,45,5,48", ListParams{}, "bbox must be"},
		{"bbox=0,0,200,1", ListParams{}, "bbox must be"},
		{"sort=id", ListParams{}, "sort must be"},
		{"order=up", ListParams{}, "order must be"},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got, err := ParseListParams(httptest.NewRequest(http.MethodGet, "/routes?"+tc.query, nil))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Errorf("got  %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestListRejectsBadParams(t *testing.T) {
	store := newMemStore()
	srv := newTestServer(t, store)
	resp, err := http.Get(srv.URL + "/routes?page_size=1000")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
