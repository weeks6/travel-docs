package apitest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weeks6/travel-docs/internal/routes"
	"github.com/weeks6/travel-docs/internal/routes/imports"
)

type jobJSON struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	Stage           string     `json:"stage"`
	Progress        int        `json:"progress"`
	Revision        int64      `json:"revision"`
	RouteID         *string    `json:"route_id"`
	Filename        string     `json:"filename"`
	Name            string     `json:"name"`
	Visibility      string     `json:"visibility"`
	SizeBytes       int64      `json:"size_bytes"`
	Attempts        int        `json:"attempts"`
	MaxAttempts     int        `json:"max_attempts"`
	CancelRequested bool       `json:"cancel_requested"`
	Error           *string    `json:"error"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// enqueue uploads a file to /import-jobs. Extra headers as "Name: value".
func (c *client) enqueue(filename, content string, fields map[string]string, headers ...string) *response {
	c.e.t.Helper()
	body, ct := multipartBody(c.e.t, filename, []byte(content), fields)
	return c.do("POST", "/import-jobs", body, append(headers, "Content-Type: "+ct)...)
}

// waitJob polls the job until it is finished (or in one of the given statuses).
func (c *client) waitJob(id string, statuses ...string) jobJSON {
	c.e.t.Helper()
	if len(statuses) == 0 {
		statuses = []string{"succeeded", "failed", "canceled"}
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var j jobJSON
		c.do("GET", "/import-jobs/"+id, nil).expect(http.StatusOK).decode(&j)
		for _, s := range statuses {
			if j.Status == s {
				return j
			}
		}
		if time.Now().After(deadline) {
			c.e.t.Fatalf("job %s still %s/%s, want %v", id, j.Status, j.Stage, statuses)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sse is an open GET /events stream.
type sse struct {
	t      *testing.T
	events chan sseEvent
	cancel context.CancelFunc
}

type sseEvent struct {
	Name string // "" for data events
	Data []byte
}

func (c *client) openEvents() *sse {
	c.e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.e.srv.URL+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		c.e.t.Fatalf("GET /events: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	s := &sse{t: c.e.t, events: make(chan sseEvent, 1000), cancel: cancel}
	go func() {
		defer resp.Body.Close()
		defer close(s.events)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Name != "" || ev.Data != nil {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "event: "):
				ev.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = []byte(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	c.e.t.Cleanup(cancel)
	if ev := s.next(); ev.Name != "ready" {
		c.e.t.Fatalf("first event = %q, want ready", ev.Name)
	}
	return s
}

func (s *sse) next() sseEvent {
	s.t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			s.t.Fatal("event stream ended")
		}
		return ev
	case <-time.After(10 * time.Second):
		s.t.Fatal("no event within 10s")
	}
	return sseEvent{}
}

// nextJob returns the next job update for id.
func (s *sse) nextJob(id string) jobJSON {
	s.t.Helper()
	for {
		ev := s.next()
		var m struct {
			Type string  `json:"type"`
			Job  jobJSON `json:"job"`
		}
		if ev.Name == "" && json.Unmarshal(ev.Data, &m) == nil && m.Type == "job" && m.Job.ID == id {
			return m.Job
		}
	}
}

func TestImportJobLifecycle(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	stream := alice.openEvents()

	var j jobJSON
	resp := alice.enqueue("ridge.gpx", sampleGPX, map[string]string{"visibility": "public"}, "Idempotency-Key: k1").
		expect(http.StatusAccepted)
	resp.decode(&j)
	if j.Status != "queued" || j.Kind != "create" || j.Filename != "ridge.gpx" || j.Visibility != "public" ||
		j.SizeBytes != int64(len(sampleGPX)) || j.MaxAttempts != imports.DefaultMaxAttempts {
		t.Errorf("queued job = %+v", j)
	}
	if resp.Header.Get("Location") != "/import-jobs/"+j.ID {
		t.Errorf("Location = %q", resp.Header.Get("Location"))
	}

	// Progress arrives over the event stream, in order, ending with success.
	var stages []string
	var last jobJSON
	for last.Status != "succeeded" {
		u := stream.nextJob(j.ID)
		if u.Revision <= last.Revision {
			t.Errorf("revision went from %d to %d", last.Revision, u.Revision)
		}
		if u.Progress < last.Progress && u.Status == last.Status {
			t.Errorf("progress went back from %d to %d", last.Progress, u.Progress)
		}
		if u.Status == "failed" {
			t.Fatalf("job failed: %+v", u)
		}
		if len(stages) == 0 || stages[len(stages)-1] != u.Stage {
			stages = append(stages, u.Stage)
		}
		last = u
	}
	if got := strings.Join(stages, ","); got != "queued,starting,parsing,analyzing,saving,done" {
		t.Errorf("stages = %s", got)
	}
	if last.Progress != 100 || last.RouteID == nil || last.Attempts != 1 || last.FinishedAt == nil {
		t.Fatalf("finished job = %+v", last)
	}

	var r routeJSON
	alice.do("GET", "/routes/"+*last.RouteID, nil).expect(http.StatusOK).decode(&r)
	if r.Name != "Ridge walk" || r.Visibility != "public" || r.PointCount != 3 || r.AscentM != 100 {
		t.Errorf("imported route = %+v", r)
	}
	if ev := alice.events("subject_id=" + r.ID); len(ev) != 1 || !strings.Contains(string(ev[0].Details), j.ID) {
		t.Errorf("route.create audit event should name the import job: %+v", ev)
	}

	t.Run("idempotency key returns the same job", func(t *testing.T) {
		var again jobJSON
		resp := alice.enqueue("ridge.gpx", sampleGPX, map[string]string{"visibility": "public"}, "Idempotency-Key: k1").
			expect(http.StatusOK)
		resp.decode(&again)
		if again.ID != j.ID || resp.Header.Get("Idempotent-Replayed") != "true" {
			t.Errorf("replay = %+v, headers %v", again, resp.Header)
		}
		resp = alice.enqueue("ridge.gpx", lineGPX("Other", 1, 1, 3, 0), nil, "Idempotency-Key: k1").
			expect(http.StatusUnprocessableEntity)
		if !strings.Contains(resp.errorMsg(), "Idempotency-Key") {
			t.Errorf("error = %q", resp.errorMsg())
		}
		// Keys belong to a user.
		bob := e.register("bob")
		bob.enqueue("ridge.gpx", sampleGPX, nil, "Idempotency-Key: k1").expect(http.StatusAccepted)
		var routes listJSON
		alice.do("GET", "/routes?scope=mine", nil).expect(200).decode(&routes)
		if routes.Total != 1 {
			t.Errorf("alice has %d routes, want 1 (no duplicate import)", routes.Total)
		}
	})

	t.Run("other users can't see the job", func(t *testing.T) {
		mallory := e.register("mallory")
		mallory.do("GET", "/import-jobs/"+j.ID, nil).expect(http.StatusNotFound)
		mallory.do("POST", "/import-jobs/"+j.ID+"/cancel", nil).expect(http.StatusNotFound)
		e.anon().do("GET", "/import-jobs", nil).expect(http.StatusUnauthorized)
	})

	t.Run("finished jobs can't be canceled but can be deleted", func(t *testing.T) {
		alice.do("POST", "/import-jobs/"+j.ID+"/cancel", nil).expect(http.StatusConflict)
		alice.do("POST", "/import-jobs/"+j.ID+"/retry", nil).expect(http.StatusConflict)
		alice.do("DELETE", "/import-jobs/"+j.ID, nil).expect(http.StatusNoContent)
		alice.do("GET", "/import-jobs/"+j.ID, nil).expect(http.StatusNotFound)
	})
}

func TestImportJobValidation(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")

	t.Run("invalid GPX fails without retries", func(t *testing.T) {
		var j jobJSON
		alice.enqueue("bad.gpx", `<gpx><trk><trkseg><trkpt lat="1" lon="1"/></trkseg></trk></gpx>`, nil).
			expect(http.StatusAccepted).decode(&j)
		j = alice.waitJob(j.ID)
		if j.Status != "failed" || j.Attempts != 1 || j.Error == nil || !strings.Contains(*j.Error, "invalid GPX") {
			t.Errorf("job = %+v", j)
		}
	})

	cases := []struct {
		name, file string
		fields     map[string]string
		headers    []string
		status     int
	}{
		{"not XML", `{"type":"Feature"}`, nil, nil, http.StatusUnprocessableEntity},
		{"bad visibility", sampleGPX, map[string]string{"visibility": "friends"}, nil, http.StatusBadRequest},
		{"long name", sampleGPX, map[string]string{"name": strings.Repeat("x", 201)}, nil, http.StatusBadRequest},
		{"bad key", sampleGPX, nil, []string{"Idempotency-Key: " + strings.Repeat("k", 300)}, http.StatusBadRequest},
		{"unknown route", sampleGPX, map[string]string{"route_id": "0190a5e0-0000-7000-8000-000000000000"}, nil, http.StatusNotFound},
		{"malformed route id", sampleGPX, map[string]string{"route_id": "nope"}, nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alice.enqueue("f.gpx", tc.file, tc.fields, tc.headers...).expect(tc.status)
		})
	}
	t.Run("missing file", func(t *testing.T) {
		body, ct := multipartBody(t, "", nil, map[string]string{"name": "x"})
		alice.do("POST", "/import-jobs", body, "Content-Type: "+ct).expect(http.StatusBadRequest)
	})
	t.Run("list filters", func(t *testing.T) {
		alice.do("GET", "/import-jobs?status=weird", nil).expect(http.StatusBadRequest)
		alice.do("GET", "/import-jobs?page_size=500", nil).expect(http.StatusBadRequest)
		var res struct {
			Items []jobJSON `json:"items"`
			Total int       `json:"total"`
		}
		alice.do("GET", "/import-jobs?status=finished", nil).expect(http.StatusOK).decode(&res)
		if res.Total != 1 || res.Items[0].Status != "failed" {
			t.Errorf("finished jobs = %+v", res)
		}
	})
}

func TestImportNewVersion(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")
	r := alice.upload("ridge", sampleGPX)
	fields := map[string]string{"route_id": r.ID}

	bob.enqueue("v2.gpx", lineGPX("v2", 46, 8, 5, 10), fields).expect(http.StatusNotFound)
	alice.do("PUT", "/routes/"+r.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "viewer"}).expect(200)
	bob.enqueue("v2.gpx", lineGPX("v2", 46, 8, 5, 10), fields).expect(http.StatusForbidden)
	alice.enqueue("v2.gpx", lineGPX("v2", 46, 8, 5, 10), fields, `If-Match: "7"`).expect(http.StatusPreconditionFailed)

	var j jobJSON
	alice.enqueue("v2.gpx", lineGPX("v2", 46, 8, 5, 10), fields, `If-Match: "1"`).expect(http.StatusAccepted).decode(&j)
	if j.Kind != "replace_geometry" || j.RouteID == nil || *j.RouteID != r.ID {
		t.Errorf("job = %+v", j)
	}
	j = alice.waitJob(j.ID)
	if j.Status != "succeeded" {
		t.Fatalf("job = %+v", j)
	}
	var updated routeJSON
	alice.do("GET", "/routes/"+r.ID, nil).expect(200).decode(&updated)
	if updated.Version != 2 || updated.PointCount != 5 || updated.Name != "Ridge walk" || updated.OriginalFilename != "v2.gpx" {
		t.Errorf("route after new version = %+v", updated)
	}

	t.Run("route changed while queued", func(t *testing.T) {
		e := setup(t, withoutWorkers())
		alice := e.register("alice")
		r := alice.upload("ridge", sampleGPX)
		var j jobJSON
		alice.enqueue("v2.gpx", lineGPX("v2", 46, 8, 5, 10), map[string]string{"route_id": r.ID}, `If-Match: "1"`).
			expect(http.StatusAccepted).decode(&j)
		alice.do("PATCH", "/routes/"+r.ID, map[string]string{"name": "Renamed"}).expect(200) // now version 2
		e.startWorkers(t)
		j = alice.waitJob(j.ID)
		if j.Status != "failed" || j.Attempts != 1 || j.Error == nil || !strings.Contains(*j.Error, "changed by someone else") {
			t.Errorf("job = %+v", j)
		}
	})
}

// startWorkers runs import workers until the test ends.
func (e *env) startWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r := imports.NewRunner(e.pool, routes.NewStore(e.pool), e.broker, discardLog(),
		imports.Config{Workers: 2, PollInterval: 50 * time.Millisecond, Lease: 2 * time.Second})
	go func() { defer close(done); r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestImportCancelRetryAndLimits(t *testing.T) {
	e := setup(t, withoutWorkers())
	alice := e.register("alice")

	var j jobJSON
	alice.enqueue("ridge.gpx", sampleGPX, nil).expect(http.StatusAccepted).decode(&j)
	alice.do("DELETE", "/import-jobs/"+j.ID, nil).expect(http.StatusConflict)
	alice.do("POST", "/import-jobs/"+j.ID+"/cancel", nil).expect(http.StatusOK).decode(&j)
	if j.Status != "canceled" || j.FinishedAt == nil {
		t.Fatalf("canceled job = %+v", j)
	}
	alice.do("POST", "/import-jobs/"+j.ID+"/cancel", nil).expect(http.StatusConflict)
	alice.do("POST", "/import-jobs/"+j.ID+"/retry", nil).expect(http.StatusOK).decode(&j)
	if j.Status != "queued" || j.Attempts != 0 || j.Error != nil || j.CancelRequested {
		t.Fatalf("retried job = %+v", j)
	}

	// At most imports.MaxActivePerUser queued or running jobs per user.
	for i := 1; i < imports.MaxActivePerUser; i++ {
		alice.enqueue(fmt.Sprintf("r%d.gpx", i), sampleGPX, nil).expect(http.StatusAccepted)
	}
	resp := alice.enqueue("one-too-many.gpx", sampleGPX, nil).expect(http.StatusTooManyRequests)
	if !strings.Contains(resp.errorMsg(), "in progress") {
		t.Errorf("error = %q", resp.errorMsg())
	}

	// Workers started later pick up everything that was queued.
	e.startWorkers(t)
	j = alice.waitJob(j.ID)
	if j.Status != "succeeded" {
		t.Errorf("job = %+v", j)
	}
	var res struct {
		Total int `json:"total"`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		alice.do("GET", "/import-jobs?status=active", nil).expect(200).decode(&res)
		if res.Total == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs still active", res.Total)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var routes listJSON
	alice.do("GET", "/routes?scope=mine", nil).expect(200).decode(&routes)
	if routes.Total != imports.MaxActivePerUser {
		t.Errorf("imported %d routes, want %d", routes.Total, imports.MaxActivePerUser)
	}
}

// bigGPX returns a GPX track with n points.
func bigGPX(n int) string {
	var b strings.Builder
	b.WriteString(`<gpx version="1.1"><trk><name>Big</name><trkseg>`)
	for i := range n {
		fmt.Fprintf(&b, `<trkpt lat="%.6f" lon="%.6f"><ele>%d</ele></trkpt>`+"\n",
			45+float64(i)*0.00001, 7+float64(i%1000)*0.00001, 500+i%300)
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return b.String()
}

func TestCancelRunningImport(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	stream := alice.openEvents()

	var j jobJSON
	alice.enqueue("big.gpx", bigGPX(150_000), nil).expect(http.StatusAccepted).decode(&j)
	for {
		u := stream.nextJob(j.ID)
		if u.Stage == "parsing" {
			break
		}
		if u.Status != "queued" && u.Status != "running" {
			t.Fatalf("job finished before it could be canceled: %+v", u)
		}
	}
	var c jobJSON
	alice.do("POST", "/import-jobs/"+j.ID+"/cancel", nil).expect(http.StatusOK).decode(&c)
	if c.Status != "running" || !c.CancelRequested {
		t.Errorf("after cancel request: %+v", c)
	}
	j = alice.waitJob(j.ID)
	if j.Status != "canceled" || j.RouteID != nil {
		t.Errorf("job = %+v", j)
	}
	var routes listJSON
	alice.do("GET", "/routes?scope=mine", nil).expect(200).decode(&routes)
	if routes.Total != 0 {
		t.Errorf("a canceled import created %d routes", routes.Total)
	}
	// A canceled job can be retried and then completes.
	alice.do("POST", "/import-jobs/"+j.ID+"/retry", nil).expect(http.StatusOK)
	if j = alice.waitJob(j.ID); j.Status != "succeeded" {
		t.Errorf("retried job = %+v", j)
	}
}

func TestLargeImport(t *testing.T) {
	if testing.Short() {
		t.Skip("large file")
	}
	e := setup(t)
	alice := e.register("alice")
	file := bigGPX(360_000) // ≈ 22 MB, over the synchronous upload limit
	if len(file) <= routes.MaxUploadSize {
		t.Fatalf("test file is only %d bytes", len(file))
	}
	var j jobJSON
	alice.enqueue("big.gpx", file, nil).expect(http.StatusAccepted).decode(&j)
	j = alice.waitJob(j.ID)
	if j.Status != "succeeded" {
		t.Fatalf("job = %+v", j)
	}
	var r routeJSON
	alice.do("GET", "/routes/"+*j.RouteID, nil).expect(200).decode(&r)
	if r.PointCount != 360_000 {
		t.Errorf("point count = %d", r.PointCount)
	}
}

func TestCrashedWorkerRecovery(t *testing.T) {
	e := setup(t, withoutWorkers())
	alice := e.register("alice")
	ctx := context.Background()

	var retried, exhausted, canceled jobJSON
	alice.enqueue("a.gpx", sampleGPX, nil).expect(http.StatusAccepted).decode(&retried)
	alice.enqueue("b.gpx", sampleGPX, nil).expect(http.StatusAccepted).decode(&exhausted)
	alice.enqueue("c.gpx", sampleGPX, nil).expect(http.StatusAccepted).decode(&canceled)
	// Pretend a worker claimed them and died: their leases ran out.
	for id, attempts := range map[string]int{retried.ID: 1, exhausted.ID: 3, canceled.ID: 1} {
		if _, err := e.pool.Exec(ctx, `UPDATE import_jobs SET status = 'running', stage = 'parsing',
			attempts = $2, lease_owner = 'dead-worker', lease_expires_at = now() - interval '1 second'
			WHERE id = $1`, id, attempts); err != nil {
			t.Fatal(err)
		}
	}
	alice.do("POST", "/import-jobs/"+canceled.ID+"/cancel", nil).expect(http.StatusOK)

	e.startWorkers(t)
	if j := alice.waitJob(retried.ID); j.Status != "succeeded" || j.Attempts != 2 {
		t.Errorf("job with expired lease = %+v, want succeeded on attempt 2", j)
	}
	if j := alice.waitJob(exhausted.ID); j.Status != "failed" || j.Error == nil || !strings.Contains(*j.Error, "stopped responding") {
		t.Errorf("job out of attempts = %+v", j)
	}
	if j := alice.waitJob(canceled.ID); j.Status != "canceled" {
		t.Errorf("job canceled while its worker was gone = %+v", j)
	}
}

func TestGracefulWorkerShutdownRequeues(t *testing.T) {
	e := setup(t, withoutWorkers())
	alice := e.register("alice")
	var j jobJSON
	alice.enqueue("big.gpx", bigGPX(150_000), nil).expect(http.StatusAccepted).decode(&j)

	stream := alice.openEvents()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r := imports.NewRunner(e.pool, routes.NewStore(e.pool), e.broker, discardLog(),
		imports.Config{Workers: 1, PollInterval: 20 * time.Millisecond, ShutdownGrace: time.Millisecond})
	go func() { defer close(done); r.Run(ctx) }()
	for stream.nextJob(j.ID).Stage != "parsing" {
	}
	cancel() // shut down with a grace period too short to finish
	<-done

	alice.do("GET", "/import-jobs/"+j.ID, nil).expect(200).decode(&j)
	if j.Status != "queued" || j.Attempts != 0 {
		t.Fatalf("job after shutdown = %+v, want queued again without using an attempt", j)
	}
	e.startWorkers(t)
	if j = alice.waitJob(j.ID); j.Status != "succeeded" || j.Attempts != 1 {
		t.Errorf("job = %+v", j)
	}
}

func TestTransientErrorsAreRetried(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	ctx := context.Background()
	// Simulate an outage: inserting a route named Flaky fails once,
	// AlwaysFlaky always fails.
	for _, sql := range []string{
		`CREATE SEQUENCE IF NOT EXISTS test_flaky_seq`,
		`CREATE OR REPLACE FUNCTION test_flaky() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.name = 'AlwaysFlaky' OR (NEW.name = 'Flaky' AND nextval('test_flaky_seq') = 1) THEN
				RAISE EXCEPTION 'simulated outage';
			END IF;
			RETURN NEW;
		END $$`,
		`CREATE TRIGGER test_flaky BEFORE INSERT ON routes FOR EACH ROW EXECUTE FUNCTION test_flaky()`,
	} {
		if _, err := e.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		e.pool.Exec(ctx, `DROP TRIGGER IF EXISTS test_flaky ON routes`)
		e.pool.Exec(ctx, `DROP FUNCTION IF EXISTS test_flaky()`)
		e.pool.Exec(ctx, `DROP SEQUENCE IF EXISTS test_flaky_seq`)
	})

	var once, always jobJSON
	alice.enqueue("a.gpx", sampleGPX, map[string]string{"name": "Flaky"}).expect(http.StatusAccepted).decode(&once)
	alice.enqueue("b.gpx", sampleGPX, map[string]string{"name": "AlwaysFlaky"}).expect(http.StatusAccepted).decode(&always)

	if j := alice.waitJob(once.ID); j.Status != "succeeded" || j.Attempts != 2 || j.Error != nil {
		t.Errorf("job failing once = %+v, want success on the second attempt", j)
	}
	j := alice.waitJob(always.ID)
	if j.Status != "failed" || j.Attempts != imports.DefaultMaxAttempts || j.Error == nil ||
		!strings.Contains(*j.Error, fmt.Sprintf("failed after %d attempts", imports.DefaultMaxAttempts)) {
		t.Errorf("job failing always = %+v", j)
	}
	// Internal details stay out of the API.
	if j.Error != nil && strings.Contains(*j.Error, "simulated outage") {
		t.Errorf("error leaks database details: %q", *j.Error)
	}
}
