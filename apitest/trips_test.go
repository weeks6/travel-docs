package apitest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type tripJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Notes     string  `json:"notes"`
	Date      string  `json:"date"`
	DurationS int     `json:"duration_s"`
	DistanceM float64 `json:"distance_m"`
	AscentM   float64 `json:"ascent_m"`
	RouteID   *string `json:"route_id"`
	RouteName *string `json:"route_name"`
}

type tripStatsJSON struct {
	Trips     int     `json:"trips"`
	DurationS int64   `json:"duration_s"`
	DistanceM float64 `json:"distance_m"`
	FirstDate *string `json:"first_date"`
	LastDate  *string `json:"last_date"`
}

// timedGPX is a track recorded on day, from 08:00 for minutes minutes.
func timedGPX(name, day string, minutes int) string {
	return fmt.Sprintf(`<gpx version="1.1"><trk><name>%s</name><trkseg>
		<trkpt lat="46.0" lon="8.0"><ele>1000</ele><time>%sT08:00:00Z</time></trkpt>
		<trkpt lat="46.01" lon="8.0"><ele>1100</ele><time>%sT08:%02d:00Z</time></trkpt>
		<trkpt lat="46.02" lon="8.0"><ele>1050</ele><time>%sT%02d:%02d:00Z</time></trkpt>
	</trkseg></trk></gpx>`, name, day, day, minutes/2, day, 8+minutes/60, minutes%60)
}

// logTrip logs a trip on an existing route.
func (c *client) logTrip(body map[string]any) *response {
	c.e.t.Helper()
	return c.do("POST", "/trips", body)
}

// uploadTrip logs a trip with a new GPX file.
func (c *client) uploadTrip(filename, content string, fields map[string]string) *response {
	c.e.t.Helper()
	body, ct := multipartBody(c.e.t, filename, []byte(content), fields)
	return c.do("POST", "/trips", body, "Content-Type: "+ct)
}

func TestTripFromExistingRoute(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	timed := alice.upload("ridge", timedGPX("Ridge", "2026-05-01", 95))
	var withDuration struct {
		DurationS *int `json:"duration_s"`
	}
	alice.do("GET", "/routes/"+timed.ID, nil).expect(http.StatusOK).decode(&withDuration)
	if withDuration.DurationS == nil || *withDuration.DurationS != 95*60 {
		t.Fatalf("route duration_s = %v, want %d", withDuration.DurationS, 95*60)
	}

	// Date, duration and name default to the recording.
	var tr tripJSON
	alice.logTrip(map[string]any{"route_id": timed.ID}).expect(http.StatusCreated).decode(&tr)
	if tr.Date != "2026-05-01" || tr.DurationS != 95*60 || tr.Name != "Ridge" || tr.DistanceM != timed.DistanceM ||
		tr.RouteID == nil || *tr.RouteID != timed.ID || tr.RouteName == nil || *tr.RouteName != "Ridge" {
		t.Fatalf("trip = %+v", tr)
	}
	// Going again later: given values win.
	alice.logTrip(map[string]any{"route_id": timed.ID, "date": "2026-06-07", "duration_s": 3600,
		"name": "Ridge again", "notes": "rainy"}).expect(http.StatusCreated).decode(&tr)
	if tr.Date != "2026-06-07" || tr.DurationS != 3600 || tr.Name != "Ridge again" || tr.Notes != "rainy" {
		t.Fatalf("trip = %+v", tr)
	}

	// A route without timestamps needs the date and duration.
	untimed := alice.upload("plain", lineGPX("Plain", 46, 8, 3, 10))
	r := alice.logTrip(map[string]any{"route_id": untimed.ID})
	if r.Status != http.StatusBadRequest || !strings.Contains(r.errorMsg(), "date is required") {
		t.Errorf("no date: %d %s", r.Status, r.Body)
	}
	r = alice.logTrip(map[string]any{"route_id": untimed.ID, "date": "2026-01-02"})
	if r.Status != http.StatusBadRequest || !strings.Contains(r.errorMsg(), "duration_s is required") {
		t.Errorf("no duration: %d %s", r.Status, r.Body)
	}
	alice.logTrip(map[string]any{"route_id": untimed.ID, "date": "2026-01-02", "duration_s": 600}).
		expect(http.StatusCreated)

	for _, bad := range []map[string]any{
		{},
		{"route_id": "not-a-uuid"},
		{"route_id": untimed.ID, "date": "2999-01-01", "duration_s": 60},
		{"route_id": untimed.ID, "date": "01/02/2026", "duration_s": 60},
		{"route_id": untimed.ID, "date": "2026-01-02", "duration_s": 0},
		{"route_id": untimed.ID, "date": "2026-01-02", "duration_s": 61 * 86400},
		{"route_id": untimed.ID, "date": "2026-01-02", "duration_s": 60, "name": strings.Repeat("x", 201)},
		{"route_id": untimed.ID, "date": "2026-01-02", "duration_s": 60, "distance_m": 5},
	} {
		if r := alice.logTrip(bad); r.Status != http.StatusBadRequest && r.Status != http.StatusNotFound {
			t.Errorf("%v: %d %s", bad, r.Status, r.Body)
		}
	}
}

func TestTripWithUploadedGPX(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	var tr tripJSON
	alice.uploadTrip("hike.gpx", timedGPX("Lake loop", "2026-04-12", 130), map[string]string{"notes": "with Bob"}).
		expect(http.StatusCreated).decode(&tr)
	if tr.Name != "Lake loop" || tr.Date != "2026-04-12" || tr.DurationS != 130*60 || tr.Notes != "with Bob" ||
		tr.RouteID == nil || tr.DistanceM <= 0 {
		t.Fatalf("trip = %+v", tr)
	}
	// The file became a private route of alice's.
	var route routeJSON
	alice.do("GET", "/routes/"+*tr.RouteID, nil).expect(http.StatusOK).decode(&route)
	if route.Visibility != "private" || route.MyRole != "owner" || route.Name != "Lake loop" {
		t.Errorf("route = %+v", route)
	}

	// Fields override the recording; the name names the route too.
	alice.uploadTrip("x.gpx", timedGPX("X", "2026-04-13", 30),
		map[string]string{"name": "Evening", "date": "2026-04-14", "duration_s": "1234", "visibility": "public"}).
		expect(http.StatusCreated).decode(&tr)
	if tr.Name != "Evening" || tr.Date != "2026-04-14" || tr.DurationS != 1234 || *tr.RouteName != "Evening" {
		t.Errorf("trip = %+v", tr)
	}

	// Invalid files and fields create nothing.
	before := alice.list("scope=mine").Total
	for _, tc := range []struct {
		content string
		fields  map[string]string
		status  int
	}{
		{"hello", nil, http.StatusUnprocessableEntity},
		{lineGPX("No times", 46, 8, 3, 0), nil, http.StatusBadRequest}, // no date to default to
		{timedGPX("T", "2026-04-13", 30), map[string]string{"duration_s": "an hour"}, http.StatusBadRequest},
		{timedGPX("T", "2026-04-13", 30), map[string]string{"visibility": "friends"}, http.StatusBadRequest},
	} {
		alice.uploadTrip("bad.gpx", tc.content, tc.fields).expect(tc.status)
	}
	if after := alice.list("scope=mine").Total; after != before {
		t.Errorf("failed uploads left routes behind: %d → %d", before, after)
	}
}

func TestTripsArePrivate(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")
	public := alice.upload("public", timedGPX("Public", "2026-05-01", 60), "visibility=public")
	private := alice.upload("private", timedGPX("Private", "2026-05-01", 60))

	var aliceTrip, bobTrip tripJSON
	alice.logTrip(map[string]any{"route_id": public.ID}).expect(http.StatusCreated).decode(&aliceTrip)
	// Anyone can log a trip on a route they can see, but not on others.
	bob.logTrip(map[string]any{"route_id": public.ID}).expect(http.StatusCreated).decode(&bobTrip)
	bob.logTrip(map[string]any{"route_id": private.ID}).expect(http.StatusNotFound)

	bob.do("GET", "/trips/"+aliceTrip.ID, nil).expect(http.StatusNotFound)
	bob.do("PATCH", "/trips/"+aliceTrip.ID, map[string]any{"name": "mine"}).expect(http.StatusNotFound)
	bob.do("DELETE", "/trips/"+aliceTrip.ID, nil).expect(http.StatusNotFound)
	var page struct {
		Items []tripJSON `json:"items"`
		Total int        `json:"total"`
	}
	bob.do("GET", "/trips", nil).expect(http.StatusOK).decode(&page)
	if page.Total != 1 || page.Items[0].ID != bobTrip.ID {
		t.Errorf("bob's trips = %+v", page)
	}
	e.anon().do("GET", "/trips", nil).expect(http.StatusUnauthorized)

	// When alice makes the route private, bob's trip keeps its stats but
	// no longer shows the route.
	alice.do("PATCH", "/routes/"+public.ID, map[string]any{"visibility": "private"}).expect(http.StatusOK)
	bob.do("GET", "/trips/"+bobTrip.ID, nil).expect(http.StatusOK).decode(&bobTrip)
	if bobTrip.RouteID != nil || bobTrip.RouteName != nil || bobTrip.DistanceM != public.DistanceM {
		t.Errorf("bob's trip after the route became private = %+v", bobTrip)
	}
}

func TestTripListStatsEditDelete(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	a := alice.upload("a", lineGPX("A", 46, 8, 3, 10))
	b := alice.upload("b", lineGPX("B", 47, 8, 5, 10))
	ids := map[string]string{}
	for _, tc := range []struct {
		route, date string
		dur         int
	}{{a.ID, "2025-12-30", 3600}, {a.ID, "2026-02-01", 1800}, {b.ID, "2026-03-15", 7200}} {
		var tr tripJSON
		alice.logTrip(map[string]any{"route_id": tc.route, "date": tc.date, "duration_s": tc.dur}).
			expect(http.StatusCreated).decode(&tr)
		ids[tc.date] = tr.ID
	}

	var page struct {
		Items      []tripJSON `json:"items"`
		Total      int        `json:"total"`
		TotalPages int        `json:"total_pages"`
	}
	alice.do("GET", "/trips?page_size=2", nil).expect(http.StatusOK).decode(&page)
	if page.Total != 3 || page.TotalPages != 2 || page.Items[0].Date != "2026-03-15" || page.Items[1].Date != "2026-02-01" {
		t.Errorf("page 1 = %+v", page)
	}
	alice.do("GET", "/trips?year=2026&route_id="+a.ID, nil).expect(http.StatusOK).decode(&page)
	if page.Total != 1 || page.Items[0].Date != "2026-02-01" {
		t.Errorf("filtered = %+v", page)
	}
	alice.do("GET", "/trips?year=twenty", nil).expect(http.StatusBadRequest)

	var st tripStatsJSON
	alice.do("GET", "/trips/stats", nil).expect(http.StatusOK).decode(&st)
	if st.Trips != 3 || st.DurationS != 12600 || *st.FirstDate != "2025-12-30" || *st.LastDate != "2026-03-15" ||
		st.DistanceM != 2*a.DistanceM+b.DistanceM {
		t.Errorf("stats = %+v", st)
	}
	alice.do("GET", "/trips/stats?year=2026", nil).expect(http.StatusOK).decode(&st)
	if st.Trips != 2 || st.DurationS != 9000 {
		t.Errorf("2026 stats = %+v", st)
	}
	alice.do("GET", "/trips/stats?year=1999", nil).expect(http.StatusOK).decode(&st)
	if st.Trips != 0 || st.DurationS != 0 || st.FirstDate != nil {
		t.Errorf("empty stats = %+v", st)
	}

	// Edit.
	id := ids["2026-02-01"]
	var tr tripJSON
	alice.do("PATCH", "/trips/"+id, map[string]any{"name": "Short one", "notes": "n", "date": "2026-02-02", "duration_s": 1900}).
		expect(http.StatusOK).decode(&tr)
	if tr.Name != "Short one" || tr.Notes != "n" || tr.Date != "2026-02-02" || tr.DurationS != 1900 {
		t.Errorf("edited = %+v", tr)
	}
	alice.do("PATCH", "/trips/"+id, map[string]any{"route_id": b.ID}).expect(http.StatusBadRequest)
	alice.do("PATCH", "/trips/"+id, map[string]any{"name": "  "}).expect(http.StatusBadRequest)
	alice.do("PATCH", "/trips/"+id, map[string]any{"date": "tomorrow"}).expect(http.StatusBadRequest)

	// Deleting the route keeps its trips.
	alice.do("DELETE", "/routes/"+a.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/trips/"+id, nil).expect(http.StatusOK).decode(&tr)
	if tr.RouteID != nil || tr.DistanceM != a.DistanceM {
		t.Errorf("trip after route deletion = %+v", tr)
	}

	alice.do("DELETE", "/trips/"+id, nil).expect(http.StatusNoContent)
	alice.do("GET", "/trips/"+id, nil).expect(http.StatusNotFound)
	alice.do("GET", "/trips/not-a-uuid", nil).expect(http.StatusNotFound)

	if got := actions(alice.events("limit=6")); !strings.HasPrefix(got, "trip.delete,route.delete,trip.update,trip.create") {
		t.Errorf("audit actions = %s", got)
	}
}
