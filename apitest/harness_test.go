// Package apitest runs end-to-end tests of the HTTP API against a real
// PostgreSQL/PostGIS database. Tests are skipped unless TEST_DATABASE_URL is
// set, e.g.:
//
//	docker compose up -d db
//	TEST_DATABASE_URL=postgres://travel:travel@localhost:5432/travel_docs_test?sslmode=disable go test ./...
//
// The tests delete all data in that database.
package apitest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/weeks6/travel-docs/db"
	"github.com/weeks6/travel-docs/jobs"
	"github.com/weeks6/travel-docs/pubsub"
	"github.com/weeks6/travel-docs/pubsub/natstest"
	"github.com/weeks6/travel-docs/server"
	"github.com/weeks6/travel-docs/services/auth"
	"github.com/weeks6/travel-docs/services/routes"
	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct horse battery"

type env struct {
	t       *testing.T
	srv     *httptest.Server
	pool    *pgxpool.Pool
	priv    []byte
	pub     []byte
	broker  pubsub.Broker
	handler *server.Server
}

// setup returns a server backed by a freshly emptied test database.
func setup(t *testing.T, options ...setupOption) *env {
	t.Helper()
	var opts setupOptions
	for _, o := range options {
		o(&opts)
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping API test")
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
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate must be a no-op: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, routes, collections, audit_events, import_jobs, trips CASCADE`); err != nil {
		t.Fatal(err)
	}

	auth.BcryptCost = bcrypt.MinCost
	priv, pub := testKeys(t)
	svc, err := auth.New(pool, auth.Config{PrivateKeyPEM: priv, PublicKeyPEM: pub})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	ctx, cancel := context.WithCancel(context.Background())
	broker, err := pubsub.ConnectNATS(natstest.Run(t).URL(), log)
	if err != nil {
		t.Fatal(err)
	}

	handler, err := server.New(server.Config{
		Pool: pool, Auth: svc, Broker: broker, Log: log,
		CORSOrigins: []string{testOrigin},
		RateLimits:  opts.rateLimits,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)

	jobCfg := jobs.Config{Workers: 2, PollInterval: 50 * time.Millisecond, Lease: 2 * time.Second,
		RetryBaseDelay: 20 * time.Millisecond, ShutdownGrace: 2 * time.Second}
	if opts.noWorkers {
		jobCfg.Workers = 0
	}
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		if !opts.noWorkers {
			jobs.NewRunner(pool, routes.NewStore(pool), broker, log, jobCfg).Run(ctx)
		}
	}()
	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		handler.CloseStreams(sctx)
		srv.Close()
		cancel()
		<-runnerDone
		broker.Close()
	})
	return &env{t: t, srv: srv, pool: pool, priv: priv, pub: pub, broker: broker, handler: handler}
}

const testOrigin = "http://localhost:3000"

type setupOptions struct {
	rateLimits server.RateLimits
	noWorkers  bool // don't process import jobs (to test queued jobs)
}

type setupOption func(*setupOptions)

func withRateLimits(r server.RateLimits) setupOption {
	return func(o *setupOptions) { o.rateLimits = r }
}

func withoutWorkers() setupOption { return func(o *setupOptions) { o.noWorkers = true } }

func testKeys(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
}

// client is an API client holding its own tokens, like an app would.
type client struct {
	e       *env
	http    *http.Client
	token   string // access token, sent as "Authorization: Bearer ..."
	refresh string // refresh token, sent in the body of /auth/refresh and /auth/logout
	user    userJSON
}

type userJSON struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

type sessionJSON struct {
	AccessToken      string   `json:"access_token"`
	TokenType        string   `json:"token_type"`
	ExpiresIn        int      `json:"expires_in"`
	RefreshToken     string   `json:"refresh_token"`
	RefreshExpiresIn int      `json:"refresh_expires_in"`
	User             userJSON `json:"user"`
}

func (e *env) anon() *client {
	return &client{e: e, http: &http.Client{}}
}

// useSession stores the tokens from a session response.
func (c *client) useSession(resp *response) sessionJSON {
	c.e.t.Helper()
	var s sessionJSON
	resp.decode(&s)
	c.token, c.refresh, c.user = s.AccessToken, s.RefreshToken, s.User
	return s
}

// login logs in as email and stores the session.
func (c *client) login(email, password string) sessionJSON {
	c.e.t.Helper()
	return c.useSession(c.do("POST", "/auth/login", map[string]string{"email": email, "password": password}).
		expect(http.StatusOK))
}

// refreshWith calls /auth/refresh with the given token and, on success,
// stores the new session.
func (c *client) refreshWith(token string) *response {
	c.e.t.Helper()
	resp := c.do("POST", "/auth/refresh", map[string]string{"refresh_token": token})
	if resp.Status == http.StatusOK {
		c.useSession(resp)
	}
	return resp
}

// refreshSession refreshes with the client's current refresh token.
func (c *client) refreshSession() *response {
	c.e.t.Helper()
	return c.refreshWith(c.refresh)
}

// register creates a user name@example.com and returns a logged-in client.
func (e *env) register(name string) *client {
	e.t.Helper()
	c := e.anon()
	resp := c.do("POST", "/auth/register", map[string]string{
		"email": name + "@example.com", "password": testPassword, "display_name": name,
	})
	resp.expect(http.StatusCreated)
	c.useSession(resp)
	return c
}

type response struct {
	t      *testing.T
	req    string
	Status int
	Header http.Header
	Body   []byte
}

func (r *response) expect(status int) *response {
	r.t.Helper()
	if r.Status != status {
		r.t.Fatalf("%s: status %d, want %d; body: %s", r.req, r.Status, status, r.Body)
	}
	return r
}

func (r *response) decode(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		r.t.Fatalf("%s: decode %s: %v", r.req, r.Body, err)
	}
}

func (r *response) errorMsg() string {
	var e struct {
		Error string `json:"error"`
	}
	json.Unmarshal(r.Body, &e)
	return e.Error
}

// do sends a request. body may be nil, []byte (sent as is) or a value to
// JSON-encode. Extra headers are given as "Name: value" strings.
func (c *client) do(method, path string, body any, headers ...string) *response {
	c.e.t.Helper()
	var rd io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			c.e.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
		contentType = "application/json"
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, rd)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for _, h := range headers {
		k, v, _ := strings.Cut(h, ":")
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
		c.e.t.Errorf("%s %s: the API must not set cookies, got %v", method, path, sc)
	}
	return &response{t: c.e.t, req: method + " " + path, Status: resp.StatusCode, Header: resp.Header, Body: data}
}

// multipart builds a multipart body with a "file" field (skipped when
// filename is empty) and extra fields.
func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) ([]byte, string) {
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
	return buf.Bytes(), mw.FormDataContentType()
}

func (c *client) uploadRaw(filename string, content []byte, fields map[string]string) *response {
	c.e.t.Helper()
	body, ct := multipartBody(c.e.t, filename, content, fields)
	return c.do("POST", "/routes", body, "Content-Type: "+ct)
}

// upload creates a route from GPX text and returns it.
func (c *client) upload(name, gpxText string, fields ...string) routeJSON {
	c.e.t.Helper()
	f := map[string]string{}
	for _, kv := range fields {
		k, v, _ := strings.Cut(kv, "=")
		f[k] = v
	}
	var r routeJSON
	c.uploadRaw(name+".gpx", []byte(gpxText), f).expect(http.StatusCreated).decode(&r)
	return r
}

type routeJSON struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	OriginalFilename string     `json:"original_filename"`
	DistanceM        float64    `json:"distance_m"`
	AscentM          float64    `json:"ascent_m"`
	DescentM         float64    `json:"descent_m"`
	MinElevationM    *float64   `json:"min_elevation_m"`
	MaxElevationM    *float64   `json:"max_elevation_m"`
	HasElevation     bool       `json:"has_elevation"`
	PointCount       int        `json:"point_count"`
	StartedAt        *time.Time `json:"started_at"`
	Visibility       string     `json:"visibility"`
	Version          int        `json:"version"`
	OwnerID          string     `json:"owner_id"`
	OwnerName        string     `json:"owner_name"`
	MyRole           string     `json:"my_role"`
}

type listJSON struct {
	Items      []routeJSON `json:"items"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	Total      int         `json:"total"`
	TotalPages int         `json:"total_pages"`
}

func (c *client) list(query string) listJSON {
	c.e.t.Helper()
	var l listJSON
	c.do("GET", "/routes?"+query, nil).expect(http.StatusOK).decode(&l)
	return l
}

func names(items []routeJSON) string {
	n := make([]string, len(items))
	for i, r := range items {
		n[i] = r.Name
	}
	return strings.Join(n, ", ")
}

// lineGPX builds a straight north-bound track of n points starting at
// (lat, lon), 0.01° apart (≈1.11 km), climbing climbPerStep meters per point.
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

const sampleGPX = `<?xml version="1.0"?>
<gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1">
  <trk><name>Ridge walk</name><desc>Along the ridge</desc><trkseg>
    <trkpt lat="46.0" lon="8.0"><ele>1000</ele><time>2026-05-01T08:00:00Z</time></trkpt>
    <trkpt lat="46.01" lon="8.0"><ele>1100</ele></trkpt>
    <trkpt lat="46.02" lon="8.0"><ele>1050</ele></trkpt>
  </trkseg></trk>
</gpx>`

type eventJSON struct {
	ID          int64           `json:"id"`
	ActorID     *string         `json:"actor_id"`
	OwnerID     *string         `json:"owner_id"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   *string         `json:"subject_id"`
	Details     json.RawMessage `json:"details"`
}

func (c *client) events(query string) []eventJSON {
	c.e.t.Helper()
	var res struct {
		Items []eventJSON `json:"items"`
	}
	c.do("GET", "/audit-events?"+query, nil).expect(http.StatusOK).decode(&res)
	return res.Items
}

func actions(events []eventJSON) string {
	a := make([]string, len(events))
	for i, e := range events {
		a[i] = e.Action
	}
	return strings.Join(a, ",")
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
