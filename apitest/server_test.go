package apitest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/weeks6/travel-docs/internal/server"
)

func TestHTTPRateLimits(t *testing.T) {
	e := setup(t, withRateLimits(server.RateLimits{PerSecond: 0.001, Burst: 4, AuthPerMinute: 0.001, AuthBurst: 2}))
	alice := e.register("alice")                   // auth request 1 (and API request 1 for this IP)
	alice.login("alice@example.com", testPassword) // auth request 2 (API request 1 for alice)
	resp := e.anon().do("POST", "/auth/login", map[string]string{"email": "alice@example.com", "password": testPassword})
	resp.expect(http.StatusTooManyRequests)
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}

	for range 3 {
		alice.do("GET", "/auth/me", nil).expect(http.StatusOK)
	}
	alice.do("GET", "/auth/me", nil).expect(http.StatusTooManyRequests)
	// API limits are per user: anonymous requests from the same address
	// have their own budget.
	e.anon().do("GET", "/routes?scope=public", nil).expect(http.StatusOK)
}

func TestCORS(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest("OPTIONS", e.srv.URL+"/routes", nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,idempotency-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != testOrigin ||
		!strings.Contains(strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers")), "idempotency-key") {
		t.Errorf("preflight: %d %v", resp.StatusCode, resp.Header)
	}

	r := e.anon().do("GET", "/routes", nil, "Origin: https://evil.example")
	if r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unknown origin allowed: %v", r.Header)
	}
	r = e.anon().do("GET", "/auth/me", nil, "Origin: "+testOrigin)
	if r.Status != http.StatusUnauthorized || r.Header.Get("Access-Control-Allow-Origin") != testOrigin {
		t.Errorf("errors need CORS headers too: %d %v", r.Status, r.Header)
	}
}
