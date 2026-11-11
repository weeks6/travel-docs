// Command loadtest drives a running Travel Docs server with simulated group
// trips: it registers users, creates active trips, connects every
// participant over WebSocket and sends location updates, measuring how many
// updates reach the other participants and how fast. Optionally it also
// sends plain HTTP requests in parallel.
//
//	go run ./cmd/loadtest -base http://localhost:8080 -trips 5 -members 10 -rate 1 -duration 1m
//
// Registration is rate limited per IP; for many users start the server with
// RATE_LIMIT_AUTH_PER_MINUTE=0 (or the tool waits as the server asks).
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type options struct {
	base     string
	trips    int
	members  int
	rate     float64
	duration time.Duration
	httpRPS  float64
	cleanup  bool
}

func main() {
	var o options
	flag.StringVar(&o.base, "base", "http://localhost:8080", "API base URL")
	flag.IntVar(&o.trips, "trips", 5, "number of trips")
	flag.IntVar(&o.members, "members", 10, "participants per trip (including the owner)")
	flag.Float64Var(&o.rate, "rate", 1, "location updates per second per participant")
	flag.DurationVar(&o.duration, "duration", 30*time.Second, "how long to send updates (keep under 14m: access tokens expire)")
	flag.Float64Var(&o.httpRPS, "http-rps", 0, "additional HTTP requests per second (GET /routes, /trips)")
	flag.BoolVar(&o.cleanup, "cleanup", true, "delete the created users (and their trips) afterwards")
	flag.Parse()
	if o.trips < 1 || o.members < 2 || o.rate <= 0 || o.duration <= 0 {
		fmt.Fprintln(os.Stderr, "need -trips ≥ 1, -members ≥ 2, -rate > 0 and -duration > 0")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

const password = "loadtest-password"

type user struct {
	id, email, token string
}

type api struct {
	base string
	http *http.Client
}

// call sends a JSON request, waiting and retrying when rate limited.
func (a *api) call(ctx context.Context, method, path, token string, body, out any) error {
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 20 {
			wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(max(wait, 1)) * time.Second):
			}
			continue
		}
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

func (a *api) register(ctx context.Context, email, name string) (user, error) {
	var s struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	err := a.call(ctx, "POST", "/auth/register", "", map[string]string{
		"email": email, "password": password, "display_name": name}, &s)
	return user{id: s.User.ID, email: email, token: s.AccessToken}, err
}

// stats collects measurements from all goroutines.
type stats struct {
	mu         sync.Mutex
	latencies  []time.Duration
	httpLat    []time.Duration
	sent       atomic.Int64
	received   atomic.Int64
	limited    atomic.Int64
	wsErrors   atomic.Int64
	httpErrors atomic.Int64
	connected  atomic.Int64
	connFailed atomic.Int64
}

func (s *stats) latency(d time.Duration) {
	s.mu.Lock()
	s.latencies = append(s.latencies, d)
	s.mu.Unlock()
}

func run(ctx context.Context, o options) error {
	a := &api{base: strings.TrimRight(o.base, "/"), http: &http.Client{Timeout: 30 * time.Second}}
	b := make([]byte, 4)
	rand.Read(b)
	runID := hex.EncodeToString(b)

	fmt.Printf("Setting up %d trips × %d participants (run %s)…\n", o.trips, o.members, runID)
	start := time.Now()
	var users []user
	defer func() {
		if !o.cleanup {
			return
		}
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for _, u := range users {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				a.call(cctx, "DELETE", "/auth/me", u.token, map[string]string{"password": password}, nil)
			})
		}
		wg.Wait()
		fmt.Printf("Deleted %d test users.\n", len(users))
	}()

	// Users, registered with limited concurrency.
	total := o.trips * o.members
	users = make([]user, total)
	var setupErr error
	var errOnce sync.Once
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range total {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			email := fmt.Sprintf("loadtest-%s-%d@example.com", runID, i)
			u, err := a.register(ctx, email, fmt.Sprintf("Load %d", i))
			if err != nil {
				errOnce.Do(func() { setupErr = err })
				return
			}
			users[i] = u
		})
	}
	wg.Wait()
	users = slices.DeleteFunc(users, func(u user) bool { return u.token == "" })
	if setupErr != nil {
		return fmt.Errorf("register users: %w", setupErr)
	}

	// Trips: the first member of each group owns it and invites the others.
	tripIDs := make([]string, o.trips)
	for t := range o.trips {
		group := users[t*o.members : (t+1)*o.members]
		var trip struct {
			ID string `json:"id"`
		}
		if err := a.call(ctx, "POST", "/trips", group[0].token, map[string]any{"name": "Load test " + runID}, &trip); err != nil {
			return err
		}
		tripIDs[t] = trip.ID
		for _, m := range group[1:] {
			var inv struct {
				ID string `json:"id"`
			}
			if err := a.call(ctx, "POST", "/trips/"+trip.ID+"/invitations", group[0].token, map[string]string{"email": m.email}, &inv); err != nil {
				return err
			}
			if err := a.call(ctx, "POST", "/trip-invitations/"+inv.ID+"/accept", m.token, nil, nil); err != nil {
				return err
			}
		}
		if err := a.call(ctx, "POST", "/trips/"+trip.ID+"/start", group[0].token, nil, nil); err != nil {
			return err
		}
	}
	fmt.Printf("Setup done in %s.\n", time.Since(start).Round(time.Millisecond))

	// Connect everyone, then send for the duration.
	st := &stats{}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Senders stop after the duration; connections stay open a little longer
	// so that updates in flight still arrive.
	sendCtx, stopSending := context.WithCancel(runCtx)
	defer stopSending()
	var conns sync.WaitGroup
	ready := make(chan struct{})
	var readyWG sync.WaitGroup
	wsBase := "ws" + strings.TrimPrefix(a.base, "http")
	for t, tripID := range tripIDs {
		for _, u := range users[t*o.members : (t+1)*o.members] {
			readyWG.Add(1)
			conns.Go(func() { participant(runCtx, sendCtx, wsBase+"/trips/"+tripID+"/ws", u, o, st, &readyWG, ready) })
		}
	}
	readyWG.Wait()
	fmt.Printf("%d WebSockets connected (%d failed). Sending %.2g updates/s per participant for %s…\n",
		st.connected.Load(), st.connFailed.Load(), o.rate, o.duration)
	close(ready)

	if o.httpRPS > 0 {
		conns.Go(func() { httpLoad(sendCtx, a, users, o.httpRPS, st) })
	}
	sendStart := time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(o.duration):
	}
	stopSending()
	elapsed := time.Since(sendStart)
	time.Sleep(2 * time.Second) // let in-flight updates arrive
	cancel()
	conns.Wait()

	report(o, st, elapsed)
	return nil
}

type serverMsg struct {
	Type       string    `json:"type"`
	UserID     string    `json:"user_id"`
	Code       string    `json:"code"`
	Error      string    `json:"error"`
	RecordedAt time.Time `json:"recorded_at"`
}

// participant connects one user and sends their positions until ctx ends.
func participant(ctx, sendCtx context.Context, url string, u user, o options, st *stats, readyWG *sync.WaitGroup, ready <-chan struct{}) {
	signalReady := sync.OnceFunc(readyWG.Done)
	defer signalReady()
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, url, nil)
	dcancel()
	if err != nil {
		st.connFailed.Add(1)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	auth, _ := json.Marshal(map[string]string{"type": "auth", "token": u.token})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		st.connFailed.Add(1)
		return
	}

	// Reader: measure delivery latency of everyone's updates.
	gotReady := make(chan struct{})
	go func() {
		readyOnce := sync.OnceFunc(func() { close(gotReady) })
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				readyOnce()
				return
			}
			var m serverMsg
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.Type {
			case "ready":
				readyOnce()
			case "location":
				st.received.Add(1)
				st.latency(time.Since(m.RecordedAt))
			case "error":
				if m.Code == "rate_limited" {
					st.limited.Add(1)
				} else {
					st.wsErrors.Add(1)
				}
			}
		}
	}()
	select {
	case <-gotReady:
	case <-time.After(15 * time.Second):
	}
	st.connected.Add(1)
	signalReady()
	<-ready

	send(sendCtx, conn, o.rate, st)
	<-ctx.Done() // keep receiving until the drain period is over
	conn.Close(websocket.StatusNormalClosure, "done")
}

// send reports a random walk at the given rate until ctx ends, starting
// at a random offset to spread the load.
func send(ctx context.Context, conn *websocket.Conn, rate float64, st *stats) {
	lat, lon := 46+mathrand.Float64(), 8+mathrand.Float64()
	interval := time.Duration(float64(time.Second) / rate)
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(mathrand.Int64N(int64(interval)))):
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		lat += (mathrand.Float64() - 0.5) * 0.0002
		lon += (mathrand.Float64() - 0.5) * 0.0002
		msg, _ := json.Marshal(map[string]any{
			"type": "location", "lat": lat, "lon": lon, "accuracy_m": 5,
			"recorded_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			if ctx.Err() == nil {
				st.wsErrors.Add(1)
			}
			return
		}
		st.sent.Add(1)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// httpLoad sends read requests at a fixed rate from random users.
func httpLoad(ctx context.Context, a *api, users []user, rps float64, st *stats) {
	tick := time.NewTicker(time.Duration(float64(time.Second) / rps))
	defer tick.Stop()
	paths := []string{"/routes?scope=public&page_size=20", "/trips?page_size=20", "/auth/me"}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		wg.Go(func() {
			u := users[mathrand.IntN(len(users))]
			start := time.Now()
			req, _ := http.NewRequestWithContext(ctx, "GET", a.base+paths[mathrand.IntN(len(paths))], nil)
			req.Header.Set("Authorization", "Bearer "+u.token)
			resp, err := a.http.Do(req)
			if err != nil {
				if ctx.Err() == nil {
					st.httpErrors.Add(1)
				}
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				st.httpErrors.Add(1)
				return
			}
			st.mu.Lock()
			st.httpLat = append(st.httpLat, time.Since(start))
			st.mu.Unlock()
		})
	}
}

func report(o options, st *stats, elapsed time.Duration) {
	sent, received := st.sent.Load(), st.received.Load()
	// Every update goes to all participants of the trip, the sender included.
	expected := sent * int64(o.members)
	fmt.Println()
	fmt.Println("Results")
	fmt.Printf("  WebSockets       %d connected, %d failed\n", st.connected.Load(), st.connFailed.Load())
	fmt.Printf("  Updates sent     %d (%.1f/s)\n", sent, float64(sent)/elapsed.Seconds())
	fmt.Printf("  Rate limited     %d\n", st.limited.Load())
	fmt.Printf("  Other WS errors  %d\n", st.wsErrors.Load())
	stored := sent - st.limited.Load()
	if stored > 0 {
		fmt.Printf("  Delivered        %d of %d expected (%.2f%%, %.1f msgs/s)\n", received, stored*int64(o.members),
			100*float64(received)/float64(stored*int64(o.members)), float64(received)/elapsed.Seconds())
	} else {
		fmt.Printf("  Delivered        %d (expected %d)\n", received, expected)
	}
	printLatency("  Fan-out latency ", st.latencies)
	if o.httpRPS > 0 {
		fmt.Printf("  HTTP requests    %d ok, %d errors\n", len(st.httpLat), st.httpErrors.Load())
		printLatency("  HTTP latency    ", st.httpLat)
	}
}

func printLatency(label string, d []time.Duration) {
	if len(d) == 0 {
		fmt.Println(label, "no samples")
		return
	}
	slices.Sort(d)
	p := func(q float64) time.Duration {
		return d[int(math.Min(float64(len(d)-1), math.Ceil(q*float64(len(d)))-1))].Round(100 * time.Microsecond)
	}
	fmt.Printf("%s p50 %s, p90 %s, p99 %s, max %s\n", label, p(.5), p(.9), p(.99), d[len(d)-1].Round(100*time.Microsecond))
}
