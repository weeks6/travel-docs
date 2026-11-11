package apitest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAuditEvents(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")

	r := alice.upload("ridge", sampleGPX)
	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "Renamed"}).expect(http.StatusOK)
	alice.do("PUT", "/routes/"+r.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}).expect(http.StatusOK)
	bob.do("PATCH", "/routes/"+r.ID, map[string]string{"description": "by bob"}).expect(http.StatusOK)

	// Alice sees everything about her route, including bob's edit.
	got := actions(alice.events("subject_id=" + r.ID))
	if got != "route.update,route.share,route.update,route.create" {
		t.Errorf("alice route events = %s", got)
	}
	// Bob sees his own actions, but not alice's account events.
	bobEvents := bob.events("")
	for _, ev := range bobEvents {
		if ev.Action == "user.register" && *ev.ActorID == alice.user.ID {
			t.Error("bob can see alice's account events")
		}
	}
	if actions(bobEvents) != "route.update,user.register" {
		t.Errorf("bob events = %s", actions(bobEvents))
	}

	var details struct {
		Fields  []string `json:"fields"`
		Version int      `json:"version"`
	}
	ev := alice.events("subject_id=" + r.ID)[0]
	json.Unmarshal(ev.Details, &details)
	if *ev.ActorID != bob.user.ID || details.Version != 3 || details.Fields[0] != "description" {
		t.Errorf("event = %+v details %s", ev, ev.Details)
	}

	// Pagination.
	for i := range 5 {
		alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": fmt.Sprint("n", i)}).expect(http.StatusOK)
	}
	var page struct {
		Items        []eventJSON `json:"items"`
		NextBeforeID int64       `json:"next_before_id"`
	}
	alice.do("GET", "/audit-events?limit=3", nil).expect(http.StatusOK).decode(&page)
	if len(page.Items) != 3 || page.NextBeforeID != page.Items[2].ID {
		t.Fatalf("page 1 = %+v", page)
	}
	lastOfPage1 := page.NextBeforeID
	alice.do("GET", fmt.Sprintf("/audit-events?limit=3&before_id=%d", lastOfPage1), nil).expect(http.StatusOK).decode(&page)
	if len(page.Items) != 3 || page.Items[0].ID >= lastOfPage1 {
		t.Errorf("page 2 must continue after page 1: %+v", page.Items)
	}

	e.anon().do("GET", "/audit-events", nil).expect(http.StatusUnauthorized)
	alice.do("GET", "/audit-events?limit=0", nil).expect(http.StatusBadRequest)
	alice.do("GET", "/audit-events?subject_id=x", nil).expect(http.StatusBadRequest)
}

// A change and its audit event are committed together: if writing the event
// fails, the change is rolled back.
func TestAuditEventsAreTransactional(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	r := alice.upload("ridge", sampleGPX)
	ctx := context.Background()

	// Make every audit insert fail.
	if _, err := e.pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
		CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_audit();`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.pool.Exec(ctx, `DROP TRIGGER IF EXISTS fail_audit ON audit_events; DROP FUNCTION IF EXISTS fail_audit();`)
	})

	alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "Should not stick"}).expect(http.StatusInternalServerError)
	alice.do("DELETE", "/routes/"+r.ID, nil).expect(http.StatusInternalServerError)
	alice.uploadRaw("x.gpx", []byte(sampleGPX), nil).expect(http.StatusInternalServerError)

	e.pool.Exec(ctx, `DROP TRIGGER fail_audit ON audit_events`)

	var got routeJSON
	alice.do("GET", "/routes/"+r.ID, nil).expect(http.StatusOK).decode(&got)
	if got.Name != "Ridge walk" || got.Version != 1 {
		t.Errorf("failed update was partially applied: %+v", got)
	}
	var versions, routes int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM route_versions WHERE route_id = $1`, r.ID).Scan(&versions)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM routes`).Scan(&routes)
	if versions != 1 || routes != 1 {
		t.Errorf("versions = %d, routes = %d; want 1, 1", versions, routes)
	}
}
