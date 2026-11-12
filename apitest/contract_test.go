package apitest

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Contract tests pin the public API: the set of endpoints and the shape
// (field names and JSON types) of every response. They exist so that
// internal refactoring can't silently change what clients see. After an
// intended API change, regenerate the golden files with:
//
//	go test ./apitest -run Contract -update
var update = flag.Bool("update", false, "rewrite golden files")

func TestContractRouteTable(t *testing.T) {
	e := setup(t)
	var lines []string
	for _, r := range e.handler.Router().Routes() {
		lines = append(lines, r.Method+" "+r.Path)
	}
	sort.Strings(lines)
	golden(t, "routes.golden", strings.Join(lines, "\n")+"\n")
}

// golden compares got with testdata/name, or rewrites it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		os.WriteFile(path+".got", []byte(got), 0o644) // for diffing; ignored by git
		t.Errorf("%s changed; if intended, rerun with -update.\n%s", path, lineDiff(string(want), got))
	}
}

// lineDiff lists lines only in want (-) or only in got (+).
func lineDiff(want, got string) string {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[l]++
		}
		return m
	}
	w, g := count(want), count(got)
	var out []string
	for l, n := range w {
		if g[l] < n {
			out = append(out, "- "+l)
		}
	}
	for l, n := range g {
		if w[l] < n {
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// shapeRecorder collects the JSON shape of responses and events.
type shapeRecorder struct {
	t   *testing.T
	buf bytes.Buffer
}

// add records the shape of a JSON document under a label.
func (s *shapeRecorder) add(label string, data []byte) {
	s.t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		s.t.Fatalf("%s: not JSON: %v: %s", label, err, data)
	}
	fmt.Fprintf(&s.buf, "== %s\n", label)
	writeShape(&s.buf, v, "")
	s.buf.WriteString("\n")
}

// resp records a response's status, contract headers and body shape.
func (s *shapeRecorder) resp(label string, r *response) {
	s.t.Helper()
	label = fmt.Sprintf("%s -> %d", label, r.Status)
	for _, h := range []string{"Location", "ETag", "Idempotent-Replayed"} {
		if r.Header.Get(h) != "" {
			label += " [" + h + "]"
		}
	}
	if len(r.Body) == 0 {
		fmt.Fprintf(&s.buf, "== %s\n(empty)\n\n", label)
		return
	}
	s.add(label, r.Body)
}

// writeShape prints field names and JSON types, recursing into objects and
// into the first element of arrays.
func writeShape(b *bytes.Buffer, v any, indent string) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("{\n")
		for _, k := range keys {
			fmt.Fprintf(b, "%s  %s: ", indent, k)
			writeShape(b, x[k], indent+"  ")
		}
		b.WriteString(indent + "}\n")
	case []any:
		if len(x) == 0 {
			b.WriteString("[]\n")
			return
		}
		b.WriteString("[")
		writeShape(b, x[0], indent)
	case string:
		b.WriteString("string\n")
	case json.Number:
		b.WriteString("number\n")
	case bool:
		b.WriteString("bool\n")
	case nil:
		b.WriteString("null\n")
	}
}

// TestContractResponseShapes walks through every endpoint and live event
// type and pins the shape of what clients receive.
func TestContractResponseShapes(t *testing.T) {
	e := setup(t)
	s := &shapeRecorder{t: t}
	alice, bob := e.register("alice"), e.register("bob")
	bobEvents := bob.openEvents()
	aliceEvents := alice.openEvents()

	// Auth.
	carol := e.anon()
	s.resp("POST /auth/register", carol.do("POST", "/auth/register", map[string]string{
		"email": "carol@example.com", "password": testPassword, "display_name": "carol"}))
	s.resp("POST /auth/login", e.anon().do("POST", "/auth/login", map[string]string{
		"email": "carol@example.com", "password": testPassword}))
	carol.login("carol@example.com", testPassword)
	s.resp("POST /auth/refresh", carol.refreshWith(carol.refresh))
	s.resp("GET /auth/me", carol.do("GET", "/auth/me", nil))
	s.resp("PATCH /auth/me", carol.do("PATCH", "/auth/me", map[string]string{"display_name": "Carol"}))
	s.resp("PUT /auth/me/password", carol.do("PUT", "/auth/me/password", map[string]string{
		"current_password": testPassword, "new_password": testPassword + "!"}))
	carol.login("carol@example.com", testPassword+"!")
	s.resp("POST /auth/logout", carol.do("POST", "/auth/logout", map[string]string{"refresh_token": carol.refresh}))
	carol.login("carol@example.com", testPassword+"!")
	s.resp("POST /auth/logout-all", carol.do("POST", "/auth/logout-all", nil))
	s.resp("DELETE /auth/me", carol.do("DELETE", "/auth/me", map[string]string{"password": testPassword + "!"}))
	s.resp("GET /auth/me unauthenticated", e.anon().do("GET", "/auth/me", nil))

	// Routes.
	body, ct := multipartBody(t, "ridge.gpx", []byte(sampleGPX), map[string]string{"visibility": "public"})
	created := alice.do("POST", "/routes", body, "Content-Type: "+ct)
	s.resp("POST /routes", created)
	var route routeJSON
	created.decode(&route)
	rp := "/routes/" + route.ID
	s.resp("GET /routes", alice.do("GET", "/routes", nil))
	s.resp("GET /routes?scope=public anonymous", e.anon().do("GET", "/routes?scope=public", nil))
	s.resp("GET /routes/:id", alice.do("GET", rp, nil))
	s.resp("GET /routes/:id not found", alice.do("GET", "/routes/00000000-0000-0000-0000-000000000000", nil))
	s.resp("PATCH /routes/:id", alice.do("PATCH", rp, map[string]string{"description": "new"}, `If-Match: "1"`))
	s.resp("PATCH /routes/:id stale", alice.do("PATCH", rp, map[string]string{"description": "x"}, `If-Match: "1"`))
	s.resp("GET /routes/:id/geometry", alice.do("GET", rp+"/geometry", nil))
	geo := []byte(`{"type":"LineString","coordinates":[[8,46,1000],[8,46.05,1200]]}`)
	s.resp("PUT /routes/:id/geometry geojson", alice.do("PUT", rp+"/geometry", geo, "Content-Type: application/geo+json"))
	body, ct = multipartBody(t, "ridge2.gpx", []byte(lineGPX("v4", 46, 8, 5, 10)), nil)
	s.resp("PUT /routes/:id/geometry gpx", alice.do("PUT", rp+"/geometry", body, "Content-Type: "+ct))
	s.resp("GET /routes/:id/elevation", alice.do("GET", rp+"/elevation", nil))
	s.resp("GET /routes/:id/versions", alice.do("GET", rp+"/versions", nil))
	s.resp("GET /routes/:id/versions/:version/geometry", alice.do("GET", rp+"/versions/1/geometry", nil))
	s.resp("POST /routes/:id/versions/:version/restore", alice.do("POST", rp+"/versions/1/restore", nil))
	s.resp("PUT /routes/:id/shares", alice.do("PUT", rp+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}))
	s.resp("GET /routes/:id/shares", alice.do("GET", rp+"/shares", nil))
	s.resp("DELETE /routes/:id/shares/:user_id", alice.do("DELETE", rp+"/shares/"+bob.user.ID, nil))

	// Collections.
	cr := alice.do("POST", "/collections", map[string]string{"name": "Alps", "description": "d"})
	s.resp("POST /collections", cr)
	var col collectionJSON
	cr.decode(&col)
	cp := "/collections/" + col.ID
	s.resp("PUT /collections/:id/routes/:route_id", alice.do("PUT", cp+"/routes/"+route.ID, nil))
	s.resp("GET /collections", alice.do("GET", "/collections", nil))
	s.resp("GET /collections/:id", alice.do("GET", cp, nil))
	s.resp("PATCH /collections/:id", alice.do("PATCH", cp, map[string]string{"name": "Alps 2"}))
	s.resp("GET /routes?collection_id", alice.do("GET", "/routes?collection_id="+col.ID, nil))
	s.resp("PUT /collections/:id/shares", alice.do("PUT", cp+"/shares", map[string]string{"email": "bob@example.com", "role": "viewer"}))
	s.resp("GET /collections/:id/shares", alice.do("GET", cp+"/shares", nil))
	s.resp("DELETE /collections/:id/shares/:user_id", alice.do("DELETE", cp+"/shares/"+bob.user.ID, nil))
	s.resp("DELETE /collections/:id/routes/:route_id", alice.do("DELETE", cp+"/routes/"+route.ID, nil))
	s.resp("DELETE /collections/:id", alice.do("DELETE", cp, nil))

	// Import jobs and the user event stream.
	jr := alice.enqueue("import.gpx", sampleGPX, map[string]string{"name": "Imported"}, "Idempotency-Key: k1")
	s.resp("POST /import-jobs", jr)
	var job jobJSON
	jr.decode(&job)
	alice.waitJob(job.ID)
	s.resp("POST /import-jobs replay", alice.enqueue("import.gpx", sampleGPX, map[string]string{"name": "Imported"}, "Idempotency-Key: k1"))
	s.add("SSE job event (succeeded)", sseJob(t, aliceEvents, job.ID, "succeeded"))
	s.resp("GET /import-jobs/:id", alice.do("GET", "/import-jobs/"+job.ID, nil))
	s.resp("GET /import-jobs", alice.do("GET", "/import-jobs", nil))
	s.resp("POST /import-jobs/:id/cancel finished", alice.do("POST", "/import-jobs/"+job.ID+"/cancel", nil))
	bad := alice.enqueue("bad.gpx", "<gpx><trk>", nil)
	var badJob jobJSON
	bad.decode(&badJob)
	alice.waitJob(badJob.ID)
	s.resp("POST /import-jobs/:id/retry", alice.do("POST", "/import-jobs/"+badJob.ID+"/retry", nil))
	alice.waitJob(badJob.ID)
	s.resp("DELETE /import-jobs/:id", alice.do("DELETE", "/import-jobs/"+job.ID, nil))
	s.resp("POST /import-jobs not XML", alice.enqueue("x.gpx", "hello", nil))

	// Trips.
	tr := alice.do("POST", "/trips", map[string]any{"name": "Hike", "route_id": route.ID})
	s.resp("POST /trips", tr)
	var trip tripJSON
	tr.decode(&trip)
	tp := "/trips/" + trip.ID
	ir := alice.do("POST", tp+"/invitations", map[string]string{"email": "bob@example.com"})
	s.resp("POST /trips/:id/invitations", ir)
	s.add("SSE invitation event", sseData(t, bobEvents, "invitation"))
	var inv invitationJSON
	ir.decode(&inv)
	s.resp("GET /trips/:id/invitations", alice.do("GET", tp+"/invitations", nil))
	s.resp("GET /trip-invitations", bob.do("GET", "/trip-invitations", nil))
	s.resp("POST /trip-invitations/:id/accept", bob.do("POST", "/trip-invitations/"+inv.ID+"/accept", nil))
	s.resp("GET /trips", alice.do("GET", "/trips", nil))
	s.resp("GET /trips/:id", bob.do("GET", tp, nil))
	s.resp("GET /trips/:id/participants", alice.do("GET", tp+"/participants", nil))
	s.resp("POST /trips/:id/start", alice.do("POST", tp+"/start", nil))

	ws := alice.dialTrip(trip.ID, alice.token)
	s.add("WS ready", wsJSON(t, ws.next("ready")))
	s.resp("POST /trips/:id/locations", bob.do("POST", tp+"/locations", map[string]any{
		"points": []map[string]any{{"lat": 46.0, "lon": 8.0, "ele": 1000, "accuracy_m": 5}}}))
	s.add("WS location", wsJSON(t, ws.next("location")))
	ws.send(map[string]any{"type": "location", "lat": 46.01, "lon": 8.0})
	ws.send(map[string]any{"type": "ping"})
	s.add("WS pong", wsJSON(t, ws.next("pong")))
	ws.send(map[string]any{"type": "bogus"})
	s.add("WS error", wsJSON(t, ws.next("error")))
	bobWS := bob.dialTrip(trip.ID, bob.token)
	bobWS.next("ready")
	s.add("WS presence", wsJSON(t, ws.next("presence")))
	s.resp("PATCH /trips/:id", alice.do("PATCH", tp, map[string]any{"description": "fun"}))
	s.add("WS trip", wsJSON(t, ws.next("trip")))
	s.resp("GET /trips/:id/history", alice.do("GET", tp+"/history", nil))
	s.resp("DELETE /trips/:id/participants/:user_id", alice.do("DELETE", tp+"/participants/"+bob.user.ID, nil))
	s.add("WS removed", wsJSON(t, ws.next("removed")))
	s.add("WS participants", wsJSON(t, ws.next("participants")))
	ir = alice.do("POST", tp+"/invitations", map[string]string{"email": "bob@example.com"})
	ir.decode(&inv)
	s.resp("DELETE /trips/:id/invitations/:invitation_id", alice.do("DELETE", tp+"/invitations/"+inv.ID, nil))
	ir = alice.do("POST", tp+"/invitations", map[string]string{"email": "bob@example.com"})
	ir.decode(&inv)
	s.resp("POST /trip-invitations/:id/decline", bob.do("POST", "/trip-invitations/"+inv.ID+"/decline", nil))
	s.resp("POST /trips/:id/end", alice.do("POST", tp+"/end", nil))
	s.resp("DELETE /trips/:id", alice.do("DELETE", tp, nil))
	s.add("WS deleted", wsJSON(t, ws.next("deleted")))

	// Audit log, deletion, misc.
	s.resp("GET /audit-events", alice.do("GET", "/audit-events", nil))
	s.resp("DELETE /routes/:id", alice.do("DELETE", rp, nil))
	s.resp("GET /", e.anon().do("GET", "/", nil))

	golden(t, "shapes.golden", s.buf.String())
}

// sseData returns the next data event of the given type.
func sseData(t *testing.T, s *sse, typ string) []byte {
	t.Helper()
	for {
		ev := s.next()
		var m struct {
			Type string `json:"type"`
		}
		if ev.Name == "" && json.Unmarshal(ev.Data, &m) == nil && m.Type == typ {
			return ev.Data
		}
	}
}

// sseJob returns the raw event for job id once it reaches status.
func sseJob(t *testing.T, s *sse, id, status string) []byte {
	t.Helper()
	for {
		data := sseData(t, s, "job")
		var m struct {
			Job jobJSON `json:"job"`
		}
		if json.Unmarshal(data, &m) == nil && m.Job.ID == id && m.Job.Status == status {
			return data
		}
	}
}

func wsJSON(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
