// Command seed fills a running Travel Docs API with sample routes from
// github.com/gps-touring/sample-gpx (see sources.go), owned by a demo user
// and grouped into collections. It uses the public API (background imports),
// so it works against any instance:
//
//	go run ./cmd/seed                              # local API, demo@example.com
//	go run ./cmd/seed -api https://… -email you@… -password …
//
// Running it again only adds what is missing: routes are matched by their
// original file name, and uploads carry an Idempotency-Key.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"time"
)

type options struct {
	api, email, password, displayName, visibility string
	collections                                   bool
	timeout                                       time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.api, "api", envOr("SEED_API", "http://localhost:8080"), "API base URL")
	flag.StringVar(&o.email, "email", envOr("SEED_EMAIL", "demo@example.com"), "account to own the routes (registered if missing)")
	flag.StringVar(&o.password, "password", envOr("SEED_PASSWORD", "demo-password"), "its password")
	flag.StringVar(&o.displayName, "name", "Demo", "display name when registering")
	flag.StringVar(&o.visibility, "visibility", "public", "visibility of the routes and collections: public or private")
	flag.BoolVar(&o.collections, "collections", true, "group the routes into collections")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up after this long")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	c := &client{base: strings.TrimRight(o.api, "/"), http: &http.Client{Timeout: 2 * time.Minute}}
	if err := c.login(ctx, o.email, o.password, o.displayName); err != nil {
		return err
	}
	existing, err := c.myRoutes(ctx)
	if err != nil {
		return fmt.Errorf("list routes: %w", err)
	}

	// Queue the missing files.
	routeIDs := map[string]string{} // repository path → route ID
	pending := map[string]string{}  // job ID → repository path
	for _, g := range groups {
		for _, s := range g.Samples {
			if id, ok := existing[path.Base(s.Path)]; ok {
				routeIDs[s.Path] = id
				continue
			}
			data, err := download(ctx, c.http, s.Path)
			if err != nil {
				return err
			}
			jobID, err := c.enqueue(ctx, s, data, o.visibility)
			if err != nil {
				return fmt.Errorf("upload %s: %w", s.Path, err)
			}
			pending[jobID] = s.Path
			fmt.Printf("queued   %s (%d KB)\n", s.Name, len(data)>>10)
		}
	}
	if len(routeIDs) > 0 {
		fmt.Printf("skipped  %d routes that already exist\n", len(routeIDs))
	}

	// Wait for the background imports.
	failed := 0
	for len(pending) > 0 {
		for id, p := range pending {
			var j job
			if err := c.do(ctx, "GET", "/import-jobs/"+id, nil, "", &j); err != nil {
				return err
			}
			switch j.Status {
			case "succeeded":
				routeIDs[p] = j.RouteID
				delete(pending, id)
				fmt.Printf("imported %s\n", p)
			case "failed", "canceled":
				failed++
				delete(pending, id)
				fmt.Printf("FAILED   %s: %s\n", p, j.Error)
			}
		}
		if len(pending) > 0 {
			if err := sleep(ctx, time.Second); err != nil {
				return fmt.Errorf("waiting for %d imports: %w", len(pending), err)
			}
		}
	}

	if o.collections {
		for _, g := range groups {
			if err := c.ensureCollection(ctx, g, routeIDs, o.visibility); err != nil {
				return fmt.Errorf("collection %q: %w", g.Collection, err)
			}
		}
	}
	fmt.Printf("done: %d sample routes for %s", len(routeIDs), o.email)
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	if failed > 0 {
		return errors.New("some imports failed")
	}
	return nil
}

// download fetches one file of the pinned commit.
func download(ctx context.Context, hc *http.Client, p string) ([]byte, error) {
	u := "https://raw.githubusercontent.com/" + sourceRepo + "/" + sourceCommit + "/" + p
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", p, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 60<<20))
}

// client calls the Travel Docs API with an access token.
type client struct {
	base  string
	http  *http.Client
	token string
}

type apiError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// do sends a request; body is JSON unless contentType is given. Rate-limited
// requests (429) are retried after the server's Retry-After.
func (c *client) do(ctx context.Context, method, p string, body any, contentType string, out any) error {
	var raw []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		raw = b
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			return err
		}
		contentType = "application/json"
	}
	for {
		err := c.once(ctx, method, p, raw, contentType, out)
		var ae *apiError
		if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests {
			return err
		}
		if err := sleep(ctx, max(ae.RetryAfter, time.Second)); err != nil {
			return err
		}
	}
}

func (c *client) once(ctx context.Context, method, p string, raw []byte, contentType string, out any, headers ...string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &apiError{Status: resp.StatusCode, Message: e.Error, RetryAfter: time.Duration(secs) * time.Second}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *client) login(ctx context.Context, email, password, name string) error {
	var s struct {
		AccessToken string `json:"access_token"`
	}
	err := c.do(ctx, "POST", "/auth/login", map[string]string{"email": email, "password": password}, "", &s)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		err = c.do(ctx, "POST", "/auth/register",
			map[string]string{"email": email, "password": password, "display_name": name}, "", &s)
		if err == nil {
			fmt.Printf("registered %s\n", email)
		}
	}
	if err != nil {
		return fmt.Errorf("log in as %s: %w", email, err)
	}
	c.token = s.AccessToken
	return nil
}

type route struct {
	ID               string `json:"id"`
	OriginalFilename string `json:"original_filename"`
}

// myRoutes maps the original file names of the user's routes to their IDs.
func (c *client) myRoutes(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for page := 1; ; page++ {
		var res struct {
			Items      []route `json:"items"`
			TotalPages int     `json:"total_pages"`
		}
		if err := c.do(ctx, "GET", fmt.Sprintf("/routes?scope=mine&page_size=100&page=%d", page), nil, "", &res); err != nil {
			return nil, err
		}
		for _, r := range res.Items {
			out[r.OriginalFilename] = r.ID
		}
		if page >= res.TotalPages {
			return out, nil
		}
	}
}

type job struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	RouteID string `json:"route_id"`
	Error   string `json:"error"`
}

// enqueue uploads a file as a background import. While the user has too
// many imports in progress (429), it waits and tries again.
func (c *client) enqueue(ctx context.Context, s sample, data []byte, visibility string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", s.Name)
	mw.WriteField("visibility", visibility)
	fw, err := mw.CreateFormFile("file", path.Base(s.Path))
	if err != nil {
		return "", err
	}
	fw.Write(data)
	mw.Close()
	key := "sample-gpx/" + sourceCommit[:12] + "/" + s.Path
	for {
		var j job
		err := c.once(ctx, "POST", "/import-jobs", buf.Bytes(), mw.FormDataContentType(), &j, "Idempotency-Key", key)
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests {
			if err := sleep(ctx, max(ae.RetryAfter, 2*time.Second)); err != nil {
				return "", err
			}
			continue
		}
		return j.ID, err
	}
}

// ensureCollection creates the group's collection if needed and adds its routes.
func (c *client) ensureCollection(ctx context.Context, g group, routeIDs map[string]string, visibility string) error {
	var res struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := c.do(ctx, "GET", "/collections?scope=mine&page_size=100", nil, "", &res); err != nil {
		return err
	}
	id := ""
	for _, col := range res.Items {
		if col.Name == g.Collection {
			id = col.ID
		}
	}
	if id == "" {
		var col struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, "POST", "/collections",
			map[string]string{"name": g.Collection, "description": g.Description, "visibility": visibility}, "", &col); err != nil {
			return err
		}
		id = col.ID
	}
	n := 0
	for _, s := range g.Samples {
		if rid := routeIDs[s.Path]; rid != "" {
			if err := c.do(ctx, "PUT", "/collections/"+url.PathEscape(id)+"/routes/"+url.PathEscape(rid), nil, "", nil); err != nil {
				return err
			}
			n++
		}
	}
	fmt.Printf("collection %q: %d routes\n", g.Collection, n)
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
