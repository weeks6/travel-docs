package routes

// Integration tests run against a real PostgreSQL/PostGIS database and are
// skipped unless TEST_DATABASE_URL is set, e.g.:
//
//	docker compose up -d db
//	TEST_DATABASE_URL=postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable go test ./...
//
// The tests delete all rows from the routes table, so never point them at a
// database with data you care about.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/db"
	"github.com/weeks6/travel-docs/gpx"
	"github.com/weeks6/travel-docs/routestats"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Running twice must be a no-op.
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM routes`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// lineGPX builds a straight north-bound track of n points starting at
// (lat, lon), 0.01° apart, climbing climbPerStep meters per point.
func lineGPX(name string, lat, lon float64, n int, climbPerStep float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<gpx version="1.1"><trk><name>%s</name><trkseg>`, name)
	for i := range n {
		fmt.Fprintf(&b, `<trkpt lat="%f" lon="%f"><ele>%f</ele></trkpt>`,
			lat+float64(i)*0.01, lon, 100+float64(i)*climbPerStep)
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return b.String()
}

func TestPostgresStoreRoundTrip(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	ctx := context.Background()

	f, err := gpx.Parse(strings.NewReader(sampleGPX))
	if err != nil {
		t.Fatal(err)
	}
	nr, err := BuildRoute(f, "ridge.gpx", "")
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.Create(ctx, nr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != nr.ID || created.CreatedAt.IsZero() {
		t.Errorf("created = %+v", created)
	}
	if created.StartedAt == nil || !created.StartedAt.Equal(*nr.StartedAt) {
		t.Errorf("started_at = %v, want %v", created.StartedAt, nr.StartedAt)
	}

	got, err := store.Get(ctx, nr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Ridge walk" || got.DistanceM != nr.DistanceM || *got.MaxElevationM != 1100 {
		t.Errorf("got = %+v", got)
	}

	geom, err := store.Geometry(ctx, nr.ID)
	if err != nil {
		t.Fatalf("Geometry: %v", err)
	}
	var g struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(geom, &g); err != nil {
		t.Fatal(err)
	}
	if g.Type != "MultiLineString" || len(g.Coordinates[0]) != 3 || g.Coordinates[0][2] != [2]float64{8, 46.02} {
		t.Errorf("geometry = %s", geom)
	}

	profile, err := store.ElevationProfile(ctx, nr.ID)
	if err != nil {
		t.Fatalf("ElevationProfile: %v", err)
	}
	if len(profile) != 3 || profile[2] != nr.Profile[2] {
		t.Errorf("profile = %+v", profile)
	}

	if err := store.Delete(ctx, nr.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, nr.ID); err != ErrNotFound {
		t.Errorf("Get after delete: err = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, nr.ID); err != ErrNotFound {
		t.Errorf("second Delete: err = %v, want ErrNotFound", err)
	}
	if _, err := store.Geometry(ctx, nr.ID); err != ErrNotFound {
		t.Errorf("Geometry after delete: err = %v", err)
	}
	if _, err := store.ElevationProfile(ctx, nr.ID); err != ErrNotFound {
		t.Errorf("ElevationProfile after delete: err = %v", err)
	}
}

func TestPostgresNoElevation(t *testing.T) {
	store := NewPostgresStore(testPool(t))
	f, err := gpx.Parse(strings.NewReader(`<gpx><rte><rtept lat="1" lon="1"/><rtept lat="1.1" lon="1"/></rte></gpx>`))
	if err != nil {
		t.Fatal(err)
	}
	nr, _ := BuildRoute(f, "flat.gpx", "")
	r, err := store.Create(context.Background(), nr)
	if err != nil {
		t.Fatal(err)
	}
	if r.MinElevationM != nil || r.MaxElevationM != nil || r.StartedAt != nil {
		t.Errorf("expected null elevation/start, got %+v", r)
	}
	profile, err := store.ElevationProfile(context.Background(), r.ID)
	if err != nil || profile == nil || len(profile) != 0 {
		t.Errorf("profile = %#v, err = %v", profile, err)
	}
}

// TestAPIListing exercises pagination, filtering and sorting through the
// HTTP API backed by PostgreSQL.
func TestAPIListing(t *testing.T) {
	srv := newTestServer(t, NewPostgresStore(testPool(t)))

	// Distance grows with point count (≈1.11 km per step); ascent = steps*climb.
	fixtures := []struct {
		name      string
		lat, lon  float64
		points    int
		climbStep float64
	}{
		{"Alps short", 46.0, 8.0, 2, 50},    // ~1.1 km, 50 m up
		{"Alps long", 46.0, 9.0, 11, 100},   // ~11 km, 1000 m up
		{"Coast flat", 43.0, 5.0, 6, 0},     // ~5.6 km, 0 m
		{"Coast 100%_off", 43.5, 5.5, 4, 2}, // ~3.3 km, 4 m up (2 m steps add up past the 3 m threshold)
		{"Highlands", 57.0, -5.0, 8, 30},    // ~7.8 km, 210 m up
	}
	ids := map[string]string{}
	for _, fx := range fixtures {
		resp := upload(t, srv, fx.name+".gpx", []byte(lineGPX(fx.name, fx.lat, fx.lon, fx.points, fx.climbStep)), nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s: status %d", fx.name, resp.StatusCode)
		}
		ids[fx.name] = decode[Route](t, resp).ID
	}

	list := func(query string) ListResult {
		t.Helper()
		resp, err := http.Get(srv.URL + "/routes?" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /routes?%s: status %d", query, resp.StatusCode)
		}
		return decode[ListResult](t, resp)
	}
	names := func(res ListResult) string {
		var n []string
		for _, r := range res.Items {
			n = append(n, r.Name)
		}
		return strings.Join(n, ", ")
	}

	tests := []struct {
		query string
		want  string
		total int
	}{
		{"sort=name&order=asc", "Alps long, Alps short, Coast 100%_off, Coast flat, Highlands", 5},
		{"sort=distance&order=desc", "Alps long, Highlands, Coast flat, Coast 100%_off, Alps short", 5},
		{"sort=ascent&order=desc&page_size=2", "Alps long, Highlands", 5},
		{"sort=distance&order=asc&page=2&page_size=2", "Coast flat, Highlands", 5},
		{"sort=distance&order=asc&page=3&page_size=2", "Alps long", 5},
		{"sort=distance&page=4&page_size=2", "", 5},
		{"name=alps&sort=name&order=asc", "Alps long, Alps short", 2},
		{"name=" + url.QueryEscape("100%_") + "&sort=name", "Coast 100%_off", 1}, // wildcards matched literally
		{"name=%25&sort=name", "Coast 100%_off", 1},
		{"min_distance_m=3000&max_distance_m=8000&sort=distance&order=asc", "Coast 100%_off, Coast flat, Highlands", 3},
		{"min_ascent_m=100&sort=ascent&order=asc", "Highlands, Alps long", 2},
		{"max_ascent_m=5&sort=name&order=asc", "Coast 100%_off, Coast flat", 2},
		{"bbox=4,42,10,48&sort=name&order=asc", "Alps long, Alps short, Coast 100%_off, Coast flat", 4},
		{"bbox=-10,50,0,60", "Highlands", 1},
		{"bbox=100,0,110,10", "", 0},
		{"name=alps&min_distance_m=5000", "Alps long", 1},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			res := list(tc.query)
			if got := names(res); got != tc.want {
				t.Errorf("names = %q, want %q", got, tc.want)
			}
			if res.Total != tc.total {
				t.Errorf("total = %d, want %d", res.Total, tc.total)
			}
		})
	}

	t.Run("pagination metadata", func(t *testing.T) {
		res := list("page=2&page_size=2")
		if res.Page != 2 || res.PageSize != 2 || res.Total != 5 || res.TotalPages != 3 {
			t.Errorf("meta = page %d size %d total %d pages %d", res.Page, res.PageSize, res.Total, res.TotalPages)
		}
	})

	t.Run("delete removes from list", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/routes/"+ids["Highlands"], nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if res := list("bbox=-10,50,0,60"); res.Total != 0 {
			t.Errorf("deleted route still listed: %s", names(res))
		}
	})

	t.Run("elevation via API", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/routes/" + ids["Alps long"] + "/elevation")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := decode[struct {
			Points []routestats.ProfilePoint `json:"points"`
		}](t, resp)
		if len(body.Points) != 11 || body.Points[10].ElevationM != 1100 {
			t.Errorf("points = %+v", body.Points)
		}
	})
}
