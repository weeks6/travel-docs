package apitest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestUploadAndRead(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")

	resp := alice.uploadRaw("ridge.gpx", []byte(sampleGPX), nil).expect(http.StatusCreated)
	var r routeJSON
	resp.decode(&r)
	if r.Name != "Ridge walk" || r.Description != "Along the ridge" || r.OriginalFilename != "ridge.gpx" ||
		r.PointCount != 3 || r.AscentM != 100 || r.DescentM != 50 || !r.HasElevation {
		t.Errorf("route = %+v", r)
	}
	if r.Visibility != "private" || r.Version != 1 || r.MyRole != "owner" || r.OwnerID != alice.user.ID || r.OwnerName != "alice" {
		t.Errorf("ownership fields = %+v", r)
	}
	if r.DistanceM < 2200 || r.DistanceM > 2250 {
		t.Errorf("distance = %v", r.DistanceM)
	}
	if resp.Header.Get("Location") != "/routes/"+r.ID || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("headers = %v", resp.Header)
	}

	geo := alice.do("GET", "/routes/"+r.ID+"/geometry", nil).expect(http.StatusOK)
	if geo.Header.Get("Content-Type") != "application/geo+json" {
		t.Errorf("content type = %q", geo.Header.Get("Content-Type"))
	}
	var feature struct {
		Type     string `json:"type"`
		Geometry struct {
			Type        string         `json:"type"`
			Coordinates [][][3]float64 `json:"coordinates"`
		} `json:"geometry"`
	}
	geo.decode(&feature)
	if feature.Type != "Feature" || feature.Geometry.Type != "MultiLineString" ||
		feature.Geometry.Coordinates[0][1] != [3]float64{8, 46.01, 1100} {
		t.Errorf("feature = %+v", feature)
	}

	var elev struct {
		Points []struct {
			DistanceM  float64 `json:"distance_m"`
			ElevationM float64 `json:"elevation_m"`
		} `json:"points"`
	}
	alice.do("GET", "/routes/"+r.ID+"/elevation", nil).expect(http.StatusOK).decode(&elev)
	if len(elev.Points) != 3 || elev.Points[1].ElevationM != 1100 {
		t.Errorf("elevation = %+v", elev.Points)
	}

	t.Run("name fallbacks", func(t *testing.T) {
		noName := `<gpx><trk><trkseg><trkpt lat="1" lon="1"/><trkpt lat="1.1" lon="1"/></trkseg></trk></gpx>`
		var r routeJSON
		alice.uploadRaw("a.gpx", []byte(sampleGPX), map[string]string{"name": "  My name "}).expect(201).decode(&r)
		if r.Name != "My name" {
			t.Errorf("override: %q", r.Name)
		}
		alice.uploadRaw("dir/evening-run.gpx", []byte(noName), nil).expect(201).decode(&r)
		if r.Name != "evening-run" || r.HasElevation || r.MinElevationM != nil {
			t.Errorf("from filename: %+v", r)
		}
		var g struct {
			Geometry struct {
				Coordinates [][][]float64 `json:"coordinates"`
			} `json:"geometry"`
		}
		alice.do("GET", "/routes/"+r.ID+"/geometry", nil).expect(200).decode(&g)
		if len(g.Geometry.Coordinates[0][0]) != 2 {
			t.Errorf("routes without elevation should return 2D coordinates: %v", g.Geometry.Coordinates)
		}
	})

	t.Run("upload errors", func(t *testing.T) {
		alice.uploadRaw("bad.gpx", []byte("<gpx><trk/></gpx>"), nil).expect(http.StatusUnprocessableEntity)
		alice.uploadRaw("", nil, map[string]string{"name": "x"}).expect(http.StatusBadRequest)
		alice.do("POST", "/routes", map[string]string{}).expect(http.StatusBadRequest)
		alice.uploadRaw("a.gpx", []byte(sampleGPX), map[string]string{"visibility": "secret"}).expect(http.StatusBadRequest)
		big := bytes.Repeat([]byte(" "), 20<<20+1)
		alice.uploadRaw("big.gpx", big, nil).expect(http.StatusRequestEntityTooLarge)
	})

	t.Run("not found", func(t *testing.T) {
		alice.do("GET", "/routes/not-a-uuid", nil).expect(http.StatusNotFound)
		alice.do("GET", "/routes/01900000-0000-7000-8000-000000000000", nil).expect(http.StatusNotFound)
	})
}

func TestVisibilityAndPermissions(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	anon := e.anon()

	private := alice.upload("private", lineGPX("Private", 46, 8, 3, 10))
	public := alice.upload("public", lineGPX("Public", 46, 9, 3, 10), "visibility=public")

	// Private routes are invisible (404, not 403) to everyone but the owner.
	for _, c := range []*client{bob, anon} {
		c.do("GET", "/routes/"+private.ID, nil).expect(http.StatusNotFound)
		c.do("GET", "/routes/"+private.ID+"/geometry", nil).expect(http.StatusNotFound)
		c.do("GET", "/routes/"+private.ID+"/elevation", nil).expect(http.StatusNotFound)
		c.do("GET", "/routes/"+private.ID+"/versions", nil).expect(http.StatusNotFound)
	}
	bob.do("PATCH", "/routes/"+private.ID, map[string]string{"name": "x"}).expect(http.StatusNotFound)
	bob.do("DELETE", "/routes/"+private.ID, nil).expect(http.StatusNotFound)

	// Public routes are readable by anyone, editable by no one else.
	var r routeJSON
	anon.do("GET", "/routes/"+public.ID, nil).expect(http.StatusOK).decode(&r)
	if r.MyRole != "viewer" {
		t.Errorf("anonymous role = %q", r.MyRole)
	}
	anon.do("GET", "/routes/"+public.ID+"/geometry", nil).expect(http.StatusOK)
	bob.do("PATCH", "/routes/"+public.ID, map[string]string{"name": "x"}).expect(http.StatusForbidden)
	bob.do("DELETE", "/routes/"+public.ID, nil).expect(http.StatusForbidden)
	anon.do("PATCH", "/routes/"+public.ID, map[string]string{"name": "x"}).expect(http.StatusUnauthorized)

	// Scopes.
	if l := anon.list(""); names(l.Items) != "Public" {
		t.Errorf("anonymous default list = %q", names(l.Items))
	}
	anon.do("GET", "/routes?scope=mine", nil).expect(http.StatusUnauthorized)
	if l := alice.list("sort=name&order=asc"); names(l.Items) != "Private, Public" {
		t.Errorf("alice mine = %q", names(l.Items))
	}
	if l := bob.list(""); l.Total != 0 {
		t.Errorf("bob mine = %q", names(l.Items))
	}
	if l := bob.list("scope=all"); names(l.Items) != "Public" {
		t.Errorf("bob all = %q", names(l.Items))
	}

	// Sharing.
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "BOB@example.com", "role": "viewer"}).
		expect(http.StatusOK)
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "carol@example.com", "role": "editor"}).
		expect(http.StatusOK)
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "nobody@example.com", "role": "viewer"}).
		expect(http.StatusNotFound)
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "alice@example.com", "role": "viewer"}).
		expect(http.StatusBadRequest)
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "admin"}).
		expect(http.StatusBadRequest)

	bob.do("GET", "/routes/"+private.ID, nil).expect(http.StatusOK).decode(&r)
	if r.MyRole != "viewer" {
		t.Errorf("bob role = %q", r.MyRole)
	}
	if l := bob.list("scope=shared"); names(l.Items) != "Private" {
		t.Errorf("bob shared = %q", names(l.Items))
	}
	bob.do("PATCH", "/routes/"+private.ID, map[string]string{"name": "x"}).expect(http.StatusForbidden)
	bob.do("GET", "/routes/"+private.ID+"/shares", nil).expect(http.StatusForbidden)
	bob.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "carol@example.com", "role": "viewer"}).
		expect(http.StatusForbidden)

	// Editors can edit content but not visibility, shares or deletion.
	carol.do("PATCH", "/routes/"+private.ID, map[string]string{"name": "Renamed by Carol"}).expect(http.StatusOK).decode(&r)
	if r.Name != "Renamed by Carol" || r.MyRole != "editor" {
		t.Errorf("carol edit = %+v", r)
	}
	carol.do("PATCH", "/routes/"+private.ID, map[string]string{"visibility": "public"}).expect(http.StatusForbidden)
	carol.do("DELETE", "/routes/"+private.ID, nil).expect(http.StatusForbidden)

	var shares struct {
		Items []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
			Role   string `json:"role"`
		} `json:"items"`
	}
	alice.do("GET", "/routes/"+private.ID+"/shares", nil).expect(http.StatusOK).decode(&shares)
	if len(shares.Items) != 2 || shares.Items[0].Email != "bob@example.com" || shares.Items[1].Role != "editor" {
		t.Errorf("shares = %+v", shares.Items)
	}

	// Changing a role, leaving and revoking.
	alice.do("PUT", "/routes/"+private.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}).
		expect(http.StatusOK)
	bob.do("PATCH", "/routes/"+private.ID, map[string]string{"description": "bob was here"}).expect(http.StatusOK)
	bob.do("DELETE", "/routes/"+private.ID+"/shares/"+carol.user.ID, nil).expect(http.StatusForbidden)
	carol.do("DELETE", "/routes/"+private.ID+"/shares/"+carol.user.ID, nil).expect(http.StatusNoContent)
	carol.do("GET", "/routes/"+private.ID, nil).expect(http.StatusNotFound)
	alice.do("DELETE", "/routes/"+private.ID+"/shares/"+bob.user.ID, nil).expect(http.StatusNoContent)
	bob.do("GET", "/routes/"+private.ID, nil).expect(http.StatusNotFound)
	alice.do("DELETE", "/routes/"+private.ID+"/shares/"+bob.user.ID, nil).expect(http.StatusNotFound)

	// Making a route public and private again.
	alice.do("PATCH", "/routes/"+private.ID, map[string]string{"visibility": "public"}).expect(http.StatusOK).decode(&r)
	if r.Visibility != "public" || r.Version != 3 {
		t.Errorf("visibility change must not create a version: %+v", r)
	}
	anon.do("GET", "/routes/"+private.ID, nil).expect(http.StatusOK)
	alice.do("PATCH", "/routes/"+private.ID, map[string]string{"visibility": "private"}).expect(http.StatusOK)
	anon.do("GET", "/routes/"+private.ID, nil).expect(http.StatusNotFound)

	// Only the owner can delete.
	alice.do("DELETE", "/routes/"+private.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/routes/"+private.ID, nil).expect(http.StatusNotFound)
	alice.do("DELETE", "/routes/"+private.ID, nil).expect(http.StatusNotFound)
}

func TestEditingAndVersionHistory(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	r := alice.upload("ridge", sampleGPX)

	// Metadata edit with optimistic concurrency.
	var updated routeJSON
	resp := alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "Ridge v2", "description": "new"}, `If-Match: "1"`).
		expect(http.StatusOK)
	resp.decode(&updated)
	if updated.Version != 2 || updated.Name != "Ridge v2" || resp.Header.Get("ETag") != `"2"` {
		t.Errorf("after edit: %+v etag %s", updated, resp.Header.Get("ETag"))
	}
	stale := alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "lost update"}, `If-Match: "1"`)
	stale.expect(http.StatusPreconditionFailed)
	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "x"}, `If-Match: banana`).expect(http.StatusBadRequest)

	// No-op edits don't create versions.
	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "Ridge v2"}).expect(http.StatusOK).decode(&updated)
	if updated.Version != 2 {
		t.Errorf("no-op edit created version %d", updated.Version)
	}
	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "  "}).expect(http.StatusBadRequest)
	alice.do("PATCH", "/routes/"+r.ID, map[string]any{"name": "x", "owner_id": "y"}).expect(http.StatusBadRequest)

	// Replace geometry with a GPX file.
	body, ct := multipartBody(t, "longer.gpx", []byte(lineGPX("ignored", 46, 8, 5, 20)), nil)
	alice.do("PUT", "/routes/"+r.ID+"/geometry", body, "Content-Type: "+ct, `If-Match: "2"`).
		expect(http.StatusOK).decode(&updated)
	if updated.Version != 3 || updated.PointCount != 5 || updated.Name != "Ridge v2" || updated.OriginalFilename != "longer.gpx" {
		t.Errorf("after GPX replace: %+v", updated)
	}
	if updated.AscentM != 80 || updated.StartedAt != nil {
		t.Errorf("stats not recomputed: %+v", updated)
	}

	// Replace geometry with GeoJSON (as a map editor would).
	geo := `{"type":"Feature","properties":{},"geometry":{"type":"LineString","coordinates":[[8,46,500],[8,46.01],[8,46.02,520]]}}`
	alice.do("PUT", "/routes/"+r.ID+"/geometry", []byte(geo), "Content-Type: application/geo+json").
		expect(http.StatusOK).decode(&updated)
	if updated.Version != 4 || updated.PointCount != 3 || updated.OriginalFilename != "longer.gpx" || updated.AscentM != 20 {
		t.Errorf("after GeoJSON replace: %+v", updated)
	}
	var g struct {
		Geometry struct {
			Coordinates [][][3]float64 `json:"coordinates"`
		} `json:"geometry"`
	}
	alice.do("GET", "/routes/"+r.ID+"/geometry", nil).expect(200).decode(&g)
	if g.Geometry.Coordinates[0][1][2] != 510 {
		t.Errorf("missing elevation should be interpolated: %v", g.Geometry.Coordinates)
	}

	for _, bad := range []string{
		`{"type":"Point","coordinates":[8,46]}`,
		`{"type":"LineString","coordinates":[[8,46]]}`,
		`{"type":"LineString","coordinates":[[8,96],[8,46]]}`,
		`{"type":"LineString","coordinates":[[8,46,1,2],[8,46]]}`,
		`not json`,
	} {
		alice.do("PUT", "/routes/"+r.ID+"/geometry", []byte(bad), "Content-Type: application/json").
			expect(http.StatusUnprocessableEntity)
	}
	alice.do("PUT", "/routes/"+r.ID+"/geometry", []byte("x"), "Content-Type: text/plain").expect(http.StatusBadRequest)

	// History.
	var versions struct {
		Items []struct {
			Version       int     `json:"version"`
			Change        string  `json:"change"`
			RestoredFrom  *int    `json:"restored_from"`
			CreatedByName *string `json:"created_by_name"`
			Name          string  `json:"name"`
			PointCount    int     `json:"point_count"`
		} `json:"items"`
	}
	alice.do("GET", "/routes/"+r.ID+"/versions", nil).expect(http.StatusOK).decode(&versions)
	var got []string
	for _, v := range versions.Items {
		got = append(got, v.Change)
	}
	if strings.Join(got, ",") != "geometry,geometry,metadata,create" || *versions.Items[0].CreatedByName != "alice" {
		t.Errorf("history = %+v", versions.Items)
	}

	var oldGeo struct {
		Properties struct {
			Version int `json:"version"`
		} `json:"properties"`
		Geometry struct {
			Coordinates [][][3]float64 `json:"coordinates"`
		} `json:"geometry"`
	}
	alice.do("GET", "/routes/"+r.ID+"/versions/1/geometry", nil).expect(http.StatusOK).decode(&oldGeo)
	if oldGeo.Properties.Version != 1 || oldGeo.Geometry.Coordinates[0][1] != [3]float64{8, 46.01, 1100} {
		t.Errorf("version 1 geometry = %+v", oldGeo)
	}
	alice.do("GET", "/routes/"+r.ID+"/versions/99/geometry", nil).expect(http.StatusNotFound)

	// Restore version 1: becomes version 5 with the original content.
	alice.do("POST", "/routes/"+r.ID+"/versions/1/restore", nil, `If-Match: "3"`).expect(http.StatusPreconditionFailed)
	alice.do("POST", "/routes/"+r.ID+"/versions/1/restore", nil).expect(http.StatusOK).decode(&updated)
	if updated.Version != 5 || updated.Name != "Ridge walk" || updated.PointCount != 3 || updated.AscentM != 100 ||
		updated.OriginalFilename != "ridge.gpx" || updated.StartedAt == nil {
		t.Errorf("after restore: %+v", updated)
	}
	alice.do("GET", "/routes/"+r.ID+"/versions", nil).expect(http.StatusOK).decode(&versions)
	if v := versions.Items[0]; v.Change != "restore" || v.RestoredFrom == nil || *v.RestoredFrom != 1 {
		t.Errorf("restore entry = %+v", v)
	}
	alice.do("POST", "/routes/"+r.ID+"/versions/42/restore", nil).expect(http.StatusNotFound)

	// Viewers can see history but not restore.
	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"visibility": "public"}).expect(http.StatusOK)
	bob := e.register("bob")
	bob.do("GET", "/routes/"+r.ID+"/versions", nil).expect(http.StatusOK)
	bob.do("POST", "/routes/"+r.ID+"/versions/2/restore", nil).expect(http.StatusForbidden)
}

func TestConcurrentEditsDontLoseUpdates(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	r := alice.upload("ridge", sampleGPX)

	// Many simultaneous renames: each must get its own version number.
	const n = 10
	done := make(chan int, n)
	for i := range n {
		go func() {
			resp := alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "edit " + string(rune('A'+i))})
			done <- resp.Status
		}()
	}
	for range n {
		if s := <-done; s != http.StatusOK {
			t.Errorf("status %d", s)
		}
	}
	var versions struct {
		Items []struct {
			Version int `json:"version"`
		} `json:"items"`
	}
	alice.do("GET", "/routes/"+r.ID+"/versions", nil).expect(200).decode(&versions)
	if len(versions.Items) != n+1 || versions.Items[0].Version != n+1 {
		t.Errorf("got %d versions, top %d; want %d", len(versions.Items), versions.Items[0].Version, n+1)
	}
}

func TestListingFiltersAndPagination(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")

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
	for _, fx := range fixtures {
		alice.upload(fx.name, lineGPX(fx.name, fx.lat, fx.lon, fx.points, fx.climbStep))
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
		{"min_distance_m=3000&max_distance_m=8000&sort=distance&order=asc", "Coast 100%_off, Coast flat, Highlands", 3},
		{"min_ascent_m=100&sort=ascent&order=asc", "Highlands, Alps long", 2},
		{"max_ascent_m=5&sort=name&order=asc", "Coast 100%_off, Coast flat", 2},
		{"bbox=4,42,10,48&sort=name&order=asc", "Alps long, Alps short, Coast 100%_off, Coast flat", 4},
		{"bbox=-10,50,0,60", "Highlands", 1},
		{"bbox=100,0,110,10", "", 0},
		{"starts_in=7.99,45.99,8.01,46.01", "Alps short", 1}, // Alps long starts at 9.0
		{"bbox=7.99,45.99,8.01,46.01", "Alps short", 1},
		{"name=alps&min_distance_m=5000", "Alps long", 1},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			res := alice.list(tc.query)
			if got := names(res.Items); got != tc.want {
				t.Errorf("names = %q, want %q", got, tc.want)
			}
			if res.Total != tc.total {
				t.Errorf("total = %d, want %d", res.Total, tc.total)
			}
		})
	}

	res := alice.list("page=2&page_size=2")
	if res.Page != 2 || res.PageSize != 2 || res.Total != 5 || res.TotalPages != 3 {
		t.Errorf("meta = %+v", res)
	}
	for _, bad := range []string{"page=0", "page_size=101", "min_distance_m=-1", "bbox=1,2,3", "starts_in=1,2", "sort=id", "order=up", "scope=everyone", "collection_id=x"} {
		alice.do("GET", "/routes?"+bad, nil).expect(http.StatusBadRequest)
	}
}

// Routes JSON must not leak internal fields like password hashes or emails.
func TestRouteJSONShape(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	r := alice.upload("ridge", sampleGPX, "visibility=public")
	resp := e.anon().do("GET", "/routes/"+r.ID, nil).expect(200)
	var m map[string]any
	json.Unmarshal(resp.Body, &m)
	for _, k := range []string{"email", "owner_email", "password_hash"} {
		if _, ok := m[k]; ok {
			t.Errorf("route JSON exposes %q", k)
		}
	}
}

// TestUploadPathsAgree checks that direct uploads and background imports
// apply the same rules to the same input.
func TestUploadPathsAgree(t *testing.T) {
	e := setup(t, withoutWorkers())
	alice := e.register("alice")
	long := strings.Repeat("n", 201)
	cases := []struct {
		name, file, content string
		fields              map[string]string
		status              int
	}{
		{"not XML", "a.gpx", "hello", nil, http.StatusUnprocessableEntity},
		{"name too long", "a.gpx", sampleGPX, map[string]string{"name": long}, http.StatusBadRequest},
		{"bad visibility", "a.gpx", sampleGPX, map[string]string{"visibility": "friends"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct := alice.uploadRaw(tc.file, []byte(tc.content), tc.fields)
			queued := alice.enqueue(tc.file, tc.content, tc.fields)
			if direct.Status != tc.status || queued.Status != tc.status || direct.errorMsg() != queued.errorMsg() {
				t.Errorf("direct %d %q, import %d %q; want both %d with the same message",
					direct.Status, direct.errorMsg(), queued.Status, queued.errorMsg(), tc.status)
			}
		})
	}
}

func TestMapClusters(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")
	alice.upload("A", lineGPX("A", 46.0, 8.0, 3, 0))
	alice.upload("B", lineGPX("B", 46.0, 8.001, 3, 0)) // starts ~80 m from A
	alice.upload("C", lineGPX("C", 46.0, 8.1, 3, 0), "visibility=public")
	alice.upload("H", lineGPX("H", 57.0, -5.0, 3, 0))
	bob.upload("Bob private", lineGPX("Bob private", 46.0, 8.05, 3, 0))
	bob.upload("Bob public", lineGPX("Bob public", 46.0, 9.0, 3, 0), "visibility=public")

	type cluster struct {
		Lat, Lon float64
		Count    int
		BBox     [4]float64
		Route    *struct {
			ID, Name  string
			DistanceM float64 `json:"distance_m"`
		}
	}
	type result struct {
		Clusters  []cluster
		Total     int
		Truncated bool
	}
	get := func(c *client, query string) result {
		t.Helper()
		var r result
		c.do("GET", "/routes/map?"+query, nil).expect(http.StatusOK).decode(&r)
		return r
	}
	// counts lists cluster sizes, or route names for single routes, in a
	// stable order.
	counts := func(r result) string {
		var s []string
		for _, c := range r.Clusters {
			if c.Route != nil {
				s = append(s, c.Route.Name)
			} else {
				s = append(s, strconv.Itoa(c.Count))
			}
		}
		sort.Strings(s)
		return strings.Join(s, ", ")
	}

	europe := "bbox=-10,40,20,60"
	alps := "bbox=7.9,45.9,8.2,46.1"
	tests := []struct {
		name  string
		c     *client
		query string
		want  string
		total int
	}{
		// Zoomed out, the Alps routes are one cluster; Bob's private route is hidden.
		{"zoomed out", alice, europe + "&zoom=4", "4, H", 5},
		// Zoomed in, A and B (80 m apart) stay together, C is on its own.
		{"zoomed in", alice, alps + "&zoom=12", "2, C", 3},
		{"anonymous sees public routes", e.anon(), europe + "&zoom=12", "Bob public, C", 2},
		{"scope", alice, europe + "&zoom=4&scope=mine", "3, H", 4},
		{"filters", alice, europe + "&zoom=4&name=bob", "Bob public", 1},
		{"outside the box", alice, "bbox=100,0,110,10&zoom=4", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := get(tc.c, tc.query)
			if got := counts(r); got != tc.want {
				t.Errorf("clusters = %q, want %q", got, tc.want)
			}
			if r.Total != tc.total || r.Truncated {
				t.Errorf("total = %d, truncated = %v; want %d, false", r.Total, r.Truncated, tc.total)
			}
		})
	}

	// A cluster's center and bbox cover its start points; a single route
	// says which route it is.
	for _, c := range get(alice, alps+"&zoom=12").Clusters {
		switch {
		case c.Count == 2:
			if c.BBox != [4]float64{8.0, 46.0, 8.001, 46.0} || c.Lon < 8.0 || c.Lon > 8.001 || c.Lat != 46.0 {
				t.Errorf("cluster = %+v", c)
			}
		case c.Route == nil || c.Route.ID == "" || c.Route.DistanceM < 2000 || c.Lon != 8.1:
			t.Errorf("single route = %+v", c)
		}
	}

	for _, bad := range []string{"zoom=4", europe, europe + "&zoom=23", europe + "&zoom=x", "bbox=1,2,3&zoom=4"} {
		alice.do("GET", "/routes/map?"+bad, nil).expect(http.StatusBadRequest)
	}
	e.anon().do("GET", "/routes/map?"+europe+"&zoom=4&scope=mine", nil).expect(http.StatusUnauthorized)
}
