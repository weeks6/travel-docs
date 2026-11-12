package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Exporter sends tracks to Travel Docs through its public API, as a
// dedicated account: new routes become background imports, changed ones a
// new version of the route they created before.
type Exporter struct {
	Base       string
	Email      string
	Password   string
	Visibility string
	HTTP       *http.Client
	token      string
}

type apiError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }

// Login logs in, registering the account the first time.
func (x *Exporter) Login(ctx context.Context) error {
	var s struct {
		AccessToken string `json:"access_token"`
	}
	creds := map[string]string{"email": x.Email, "password": x.Password}
	err := x.call(ctx, "POST", "/auth/login", jsonBody(creds), &s)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		creds["display_name"] = "Route scraper"
		err = x.call(ctx, "POST", "/auth/register", jsonBody(creds), &s)
	}
	if err != nil {
		return fmt.Errorf("log in as %s: %w", x.Email, err)
	}
	x.token = s.AccessToken
	return nil
}

// Export imports the track and returns the route ID. With routeID, the
// track becomes a new version of that route.
func (x *Exporter) Export(ctx context.Context, t Track, routeID string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if routeID != "" {
		mw.WriteField("route_id", routeID)
	} else {
		mw.WriteField("name", t.Name)
		mw.WriteField("visibility", x.Visibility)
	}
	fw, err := mw.CreateFormFile("file", strings.NewReplacer(":", "-").Replace(t.SourceID)+".gpx")
	if err != nil {
		return "", err
	}
	fw.Write(t.GPX())
	mw.Close()

	// The key makes a retried upload (e.g. after a timeout) a no-op.
	key := "scraper/" + t.SourceID + "/" + t.Fingerprint()[:16]
	var job struct {
		ID string `json:"id"`
	}
	req := body{data: buf.Bytes(), contentType: mw.FormDataContentType(), headers: map[string]string{"Idempotency-Key": key}}
	if err := x.call(ctx, "POST", "/import-jobs", req, &job); err != nil {
		var ae *apiError
		if routeID != "" && errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return x.Export(ctx, t, "") // the route was deleted in the app: import it again
		}
		return "", err
	}
	for {
		var j struct {
			Status  string `json:"status"`
			RouteID string `json:"route_id"`
			Error   string `json:"error"`
		}
		if err := x.call(ctx, "GET", "/import-jobs/"+job.ID, body{}, &j); err != nil {
			return "", err
		}
		switch j.Status {
		case "succeeded":
			if routeID == "" {
				if err := x.describe(ctx, j.RouteID, t); err != nil {
					return j.RouteID, fmt.Errorf("set description: %w", err)
				}
			}
			return j.RouteID, nil
		case "failed", "canceled":
			return "", fmt.Errorf("import %s: %s", j.Status, j.Error)
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return "", err
		}
	}
}

// describe sets the route description: the source's text, a link back and
// the attribution its license requires.
func (x *Exporter) describe(ctx context.Context, routeID string, t Track) error {
	var r struct {
		Version int `json:"version"`
	}
	if err := x.call(ctx, "GET", "/routes/"+routeID, body{}, &r); err != nil {
		return err
	}
	parts := []string{}
	if t.Description != "" {
		parts = append(parts, t.Description)
	}
	parts = append(parts, "Source: "+t.URL, t.Attribution)
	desc := strings.Join(parts, "\n\n")
	if len([]rune(desc)) > 5000 {
		desc = string([]rune(desc)[:5000])
	}
	req := jsonBody(map[string]string{"description": desc})
	req.headers = map[string]string{"If-Match": strconv.Quote(strconv.Itoa(r.Version))}
	return x.call(ctx, "PATCH", "/routes/"+routeID, req, nil)
}

type body struct {
	data        []byte
	contentType string
	headers     map[string]string
}

func jsonBody(v any) body {
	b, _ := json.Marshal(v)
	return body{data: b, contentType: "application/json"}
}

// call sends a request. Rate limits and "too many imports in progress"
// (both 429) are waited out.
func (x *Exporter) call(ctx context.Context, method, path string, b body, out any) error {
	for {
		err := x.once(ctx, method, path, b, out)
		var ae *apiError
		if !errors.As(err, &ae) || ae.Status != http.StatusTooManyRequests {
			return err
		}
		if err := sleep(ctx, max(ae.RetryAfter, 2*time.Second)); err != nil {
			return err
		}
	}
}

func (x *Exporter) once(ctx context.Context, method, path string, b body, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, x.Base+path, bytes.NewReader(b.data))
	if err != nil {
		return err
	}
	if b.contentType != "" {
		req.Header.Set("Content-Type", b.contentType)
	}
	if x.token != "" {
		req.Header.Set("Authorization", "Bearer "+x.token)
	}
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	resp, err := x.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized && x.token != "" && path != "/auth/login" {
		// The access token expired during a long run: log in again and retry.
		x.token = ""
		if err := x.Login(ctx); err != nil {
			return err
		}
		return x.once(ctx, method, path, b, out)
	}
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

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
