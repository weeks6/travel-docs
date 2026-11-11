package apitest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/weeks6/travel-docs/server"
	"github.com/weeks6/travel-docs/services/trips"
)

type tripJSON struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	Status                string     `json:"status"`
	RouteID               *string    `json:"route_id"`
	RouteName             *string    `json:"route_name"`
	LocationRetentionDays int        `json:"location_retention_days"`
	OwnerID               string     `json:"owner_id"`
	MyRole                string     `json:"my_role"`
	ParticipantCount      int        `json:"participant_count"`
	StartedAt             *time.Time `json:"started_at"`
	EndedAt               *time.Time `json:"ended_at"`
}

type invitationJSON struct {
	ID          string `json:"id"`
	TripID      string `json:"trip_id"`
	TripName    string `json:"trip_name"`
	InviteeID   string `json:"invitee_id"`
	InvitedByID string `json:"invited_by_id"`
	Status      string `json:"status"`
}

type participantJSON struct {
	UserID       string `json:"user_id"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Online       bool   `json:"online"`
	LastPosition *struct {
		Lat        float64   `json:"lat"`
		Lon        float64   `json:"lon"`
		RecordedAt time.Time `json:"recorded_at"`
	} `json:"last_position"`
}

func (c *client) createTrip(name string, extra ...map[string]any) tripJSON {
	c.e.t.Helper()
	body := map[string]any{"name": name}
	for _, m := range extra {
		for k, v := range m {
			body[k] = v
		}
	}
	var t tripJSON
	c.do("POST", "/trips", body).expect(http.StatusCreated).decode(&t)
	return t
}

// join invites the user to the owner's trip and accepts the invitation.
func (c *client) join(owner *client, tripID string) {
	c.e.t.Helper()
	var inv invitationJSON
	owner.do("POST", "/trips/"+tripID+"/invitations", map[string]string{"email": c.user.Email}).
		expect(http.StatusCreated).decode(&inv)
	c.do("POST", "/trip-invitations/"+inv.ID+"/accept", nil).expect(http.StatusOK)
}

func (c *client) participants(tripID string) []participantJSON {
	c.e.t.Helper()
	var res struct {
		Items []participantJSON `json:"items"`
	}
	c.do("GET", "/trips/"+tripID+"/participants", nil).expect(http.StatusOK).decode(&res)
	return res.Items
}

func TestTripsAndInvitations(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	route := alice.upload("ridge", sampleGPX)

	trip := alice.createTrip("Alps", map[string]any{"route_id": route.ID, "location_retention_days": 7})
	if trip.Status != "planned" || trip.MyRole != "owner" || trip.ParticipantCount != 1 ||
		trip.RouteID == nil || *trip.RouteID != route.ID || *trip.RouteName != "Ridge walk" || trip.LocationRetentionDays != 7 {
		t.Fatalf("trip = %+v", trip)
	}
	alice.do("POST", "/trips", map[string]any{"name": " "}).expect(http.StatusBadRequest)
	alice.do("POST", "/trips", map[string]any{"name": "x", "location_retention_days": 0}).expect(http.StatusBadRequest)
	bobRoute := bob.upload("secret", sampleGPX)
	alice.do("POST", "/trips", map[string]any{"name": "x", "route_id": bobRoute.ID}).expect(http.StatusNotFound)

	// Outsiders can't see the trip.
	bob.do("GET", "/trips/"+trip.ID, nil).expect(http.StatusNotFound)
	bob.do("GET", "/trips/"+trip.ID+"/participants", nil).expect(http.StatusNotFound)
	e.anon().do("GET", "/trips", nil).expect(http.StatusUnauthorized)

	// Invitations.
	bobEvents := bob.openEvents()
	var inv invitationJSON
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "BOB@example.com"}).
		expect(http.StatusCreated).decode(&inv)
	if inv.Status != "pending" || inv.InviteeID != bob.user.ID || inv.TripName != "Alps" {
		t.Errorf("invitation = %+v", inv)
	}
	if ev := bobEvents.next(); !strings.Contains(string(ev.Data), `"type":"invitation"`) || !strings.Contains(string(ev.Data), inv.ID) {
		t.Errorf("bob's event = %s", ev.Data)
	}
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "bob@example.com"}).expect(http.StatusConflict)
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "nobody@example.com"}).expect(http.StatusNotFound)
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "alice@example.com"}).expect(http.StatusConflict)

	var mine struct {
		Items []invitationJSON `json:"items"`
	}
	bob.do("GET", "/trip-invitations", nil).expect(http.StatusOK).decode(&mine)
	if len(mine.Items) != 1 || mine.Items[0].ID != inv.ID {
		t.Fatalf("bob's invitations = %+v", mine.Items)
	}
	carol.do("POST", "/trip-invitations/"+inv.ID+"/accept", nil).expect(http.StatusNotFound)
	bob.do("POST", "/trip-invitations/"+inv.ID+"/accept", nil).expect(http.StatusOK).decode(&inv)
	if inv.Status != "accepted" {
		t.Errorf("accepted invitation = %+v", inv)
	}
	bob.do("POST", "/trip-invitations/"+inv.ID+"/decline", nil).expect(http.StatusConflict)

	var bobTrip tripJSON
	bob.do("GET", "/trips/"+trip.ID, nil).expect(http.StatusOK).decode(&bobTrip)
	if bobTrip.MyRole != "member" || bobTrip.ParticipantCount != 2 {
		t.Errorf("bob's view = %+v", bobTrip)
	}
	if bobTrip.RouteID != nil || bobTrip.RouteName != nil {
		t.Errorf("bob must not see alice's private route: %+v", bobTrip)
	}

	// Members can't manage the trip.
	bob.do("PATCH", "/trips/"+trip.ID, map[string]any{"name": "Mine"}).expect(http.StatusForbidden)
	bob.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusForbidden)
	bob.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "carol@example.com"}).expect(http.StatusForbidden)
	bob.do("GET", "/trips/"+trip.ID+"/invitations", nil).expect(http.StatusForbidden)
	bob.do("DELETE", "/trips/"+trip.ID+"/participants/"+alice.user.ID, nil).expect(http.StatusForbidden)
	bob.do("DELETE", "/trips/"+trip.ID, nil).expect(http.StatusForbidden)

	// Decline and revoke.
	var carolInv invitationJSON
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "carol@example.com"}).
		expect(http.StatusCreated).decode(&carolInv)
	alice.do("DELETE", "/trips/"+trip.ID+"/invitations/"+carolInv.ID, nil).expect(http.StatusNoContent)
	carol.do("POST", "/trip-invitations/"+carolInv.ID+"/accept", nil).expect(http.StatusConflict)
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "carol@example.com"}).
		expect(http.StatusCreated).decode(&carolInv)
	carol.do("POST", "/trip-invitations/"+carolInv.ID+"/decline", nil).expect(http.StatusOK)
	carol.do("GET", "/trips/"+trip.ID, nil).expect(http.StatusNotFound)

	var invs struct {
		Items []invitationJSON `json:"items"`
	}
	alice.do("GET", "/trips/"+trip.ID+"/invitations", nil).expect(http.StatusOK).decode(&invs)
	got := []string{}
	for _, i := range invs.Items {
		got = append(got, i.Status)
	}
	if strings.Join(got, ",") != "declined,revoked,accepted" {
		t.Errorf("invitation statuses = %v", got)
	}

	// Lifecycle.
	alice.do("POST", "/trips/"+trip.ID+"/end", nil).expect(http.StatusConflict)
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusOK).decode(&trip)
	if trip.Status != "active" || trip.StartedAt == nil {
		t.Errorf("started trip = %+v", trip)
	}
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusConflict)

	var list struct {
		Items []tripJSON `json:"items"`
		Total int        `json:"total"`
	}
	bob.do("GET", "/trips?status=active", nil).expect(http.StatusOK).decode(&list)
	if list.Total != 1 || list.Items[0].ID != trip.ID {
		t.Errorf("bob's active trips = %+v", list)
	}
	bob.do("GET", "/trips?status=planned", nil).expect(http.StatusOK).decode(&list)
	if list.Total != 0 {
		t.Errorf("bob's planned trips = %+v", list)
	}

	// Owner can't leave; members can.
	alice.do("DELETE", "/trips/"+trip.ID+"/participants/"+alice.user.ID, nil).expect(http.StatusBadRequest)
	bob.do("DELETE", "/trips/"+trip.ID+"/participants/"+bob.user.ID, nil).expect(http.StatusNoContent)
	bob.do("GET", "/trips/"+trip.ID, nil).expect(http.StatusNotFound)

	alice.do("PATCH", "/trips/"+trip.ID, map[string]any{"route_id": ""}).expect(http.StatusOK).decode(&trip)
	if trip.RouteID != nil {
		t.Errorf("route not removed: %+v", trip)
	}
	alice.do("POST", "/trips/"+trip.ID+"/end", nil).expect(http.StatusOK).decode(&trip)
	if trip.Status != "ended" || trip.EndedAt == nil {
		t.Errorf("ended trip = %+v", trip)
	}
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusConflict)
	alice.do("POST", "/trips/"+trip.ID+"/invitations", map[string]string{"email": "bob@example.com"}).expect(http.StatusConflict)

	if ev := alice.events(""); !strings.HasPrefix(actions(ev), "trip.end,trip.update,trip.leave,trip.start,trip.invite_decline,trip.invite") {
		t.Errorf("audit events = %s", actions(ev))
	}
	alice.do("DELETE", "/trips/"+trip.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/trips/"+trip.ID, nil).expect(http.StatusNotFound)
}

func TestTripLocationsOverHTTP(t *testing.T) {
	e := setup(t)
	alice, bob := e.register("alice"), e.register("bob")
	trip := alice.createTrip("Hike")
	bob.join(alice, trip.ID)

	point := map[string]any{"points": []map[string]any{{"lat": 46.1, "lon": 8.1}}}
	bob.do("POST", "/trips/"+trip.ID+"/locations", point).expect(http.StatusConflict) // not started
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusOK)

	now := time.Now().UTC()
	pts := []map[string]any{}
	for i := range 5 {
		pts = append(pts, map[string]any{
			"lat": 46 + float64(i)*0.001, "lon": 8.0, "ele": 1000 + i, "accuracy_m": 5,
			"recorded_at": now.Add(time.Duration(i-5) * time.Minute).Format(time.RFC3339Nano),
		})
	}
	bob.do("POST", "/trips/"+trip.ID+"/locations", map[string]any{"points": pts}).expect(http.StatusAccepted)
	// An older point doesn't replace the last known position.
	old := map[string]any{"points": []map[string]any{{"lat": 1, "lon": 1, "recorded_at": now.Add(-time.Hour).Format(time.RFC3339)}}}
	bob.do("POST", "/trips/"+trip.ID+"/locations", old).expect(http.StatusAccepted)

	for _, p := range alice.participants(trip.ID) {
		if p.UserID != bob.user.ID {
			continue
		}
		if p.LastPosition == nil || p.LastPosition.Lat != 46.004 || p.Online {
			t.Errorf("bob = %+v", p)
		}
	}

	bad := []map[string]any{
		{"points": []map[string]any{}},
		{"points": []map[string]any{{"lat": 100, "lon": 0}}},
		{"points": []map[string]any{{"lat": 1, "lon": 1, "recorded_at": now.Add(48 * time.Hour).Format(time.RFC3339)}}},
		{"points": []map[string]any{{"lat": 1, "lon": 1, "color": "red"}}},
	}
	for _, b := range bad {
		bob.do("POST", "/trips/"+trip.ID+"/locations", b).expect(http.StatusBadRequest)
	}
	e.register("carol").do("POST", "/trips/"+trip.ID+"/locations", point).expect(http.StatusNotFound)

	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Geometry struct {
				Type        string       `json:"type"`
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				UserID     string `json:"user_id"`
				PointCount int    `json:"point_count"`
			} `json:"properties"`
		} `json:"features"`
	}
	resp := alice.do("GET", "/trips/"+trip.ID+"/history", nil).expect(http.StatusOK)
	resp.decode(&fc)
	if resp.Header.Get("Content-Type") != "application/geo+json" || len(fc.Features) != 1 {
		t.Fatalf("history = %s", resp.Body)
	}
	f := fc.Features[0]
	if f.Geometry.Type != "LineString" || f.Properties.UserID != bob.user.ID || f.Properties.PointCount != 6 ||
		f.Geometry.Coordinates[0] != [2]float64{1, 1} || f.Geometry.Coordinates[5] != [2]float64{8, 46.004} {
		t.Errorf("track = %+v", f)
	}
	alice.do("GET", "/trips/"+trip.ID+"/history?max_points=3", nil).expect(http.StatusOK).decode(&fc)
	if n := len(fc.Features[0].Geometry.Coordinates); n > 4 || n < 2 {
		t.Errorf("downsampled to %d points", n)
	}
	alice.do("GET", "/trips/"+trip.ID+"/history?since="+now.Add(-3*time.Minute).Format(time.RFC3339), nil).
		expect(http.StatusOK).decode(&fc)
	if n := len(fc.Features[0].Geometry.Coordinates); n != 3 {
		t.Errorf("since filter returned %d points, want 3", n)
	}
	alice.do("GET", "/trips/"+trip.ID+"/history?since=yesterday", nil).expect(http.StatusBadRequest)

	// Leaving deletes your location history.
	bob.do("DELETE", "/trips/"+trip.ID+"/participants/"+bob.user.ID, nil).expect(http.StatusNoContent)
	alice.do("GET", "/trips/"+trip.ID+"/history", nil).expect(http.StatusOK).decode(&fc)
	if len(fc.Features) != 0 {
		t.Errorf("history after bob left = %+v", fc.Features)
	}
}

func TestLocationRetention(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	trip := alice.createTrip("Short memory", map[string]any{"location_retention_days": 1})
	keep := alice.createTrip("Long memory", map[string]any{"location_retention_days": 30})
	for _, id := range []string{trip.ID, keep.ID} {
		alice.do("POST", "/trips/"+id+"/start", nil).expect(http.StatusOK)
		alice.do("POST", "/trips/"+id+"/locations", map[string]any{"points": []map[string]any{{"lat": 1, "lon": 1}}}).
			expect(http.StatusAccepted)
	}
	ctx := context.Background()
	// Pretend the points arrived (and were recorded) two days ago.
	if _, err := e.pool.Exec(ctx, `UPDATE trip_locations SET received_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE trip_participants SET last_recorded_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	deleted, err := trips.NewStore(e.pool).ApplyRetention(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("ApplyRetention = %d, %v; want 1 deleted", deleted, err)
	}
	if p := alice.participants(trip.ID)[0]; p.LastPosition != nil {
		t.Errorf("last position older than the retention period is still shown: %+v", p.LastPosition)
	}
	if p := alice.participants(keep.ID)[0]; p.LastPosition == nil {
		t.Error("last position within the retention period was removed")
	}
}

// wsClient is a test WebSocket connection to a trip.
type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
	msgs chan map[string]any
	err  chan error
}

func (c *client) dialTrip(tripID string, token string) *wsClient {
	c.e.t.Helper()
	url := "ws" + strings.TrimPrefix(c.e.srv.URL, "http") + "/trips/" + tripID + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {testOrigin}}})
	if err != nil {
		c.e.t.Fatalf("dial: %v", err)
	}
	ws := &wsClient{t: c.e.t, conn: conn, msgs: make(chan map[string]any, 100), err: make(chan error, 1)}
	c.e.t.Cleanup(func() { conn.CloseNow() })
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				ws.err <- err
				close(ws.msgs)
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			ws.msgs <- m
		}
	}()
	if token != "" {
		ws.send(map[string]any{"type": "auth", "token": token})
	}
	return ws
}

func (ws *wsClient) send(v any) {
	ws.t.Helper()
	data, _ := json.Marshal(v)
	if err := ws.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		ws.t.Fatalf("write: %v", err)
	}
}

// next returns the next message of the given type, skipping others.
func (ws *wsClient) next(typ string) map[string]any {
	ws.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-ws.msgs:
			if !ok {
				ws.t.Fatalf("connection closed while waiting for %q: %v", typ, <-ws.err)
			}
			if m["type"] == typ {
				return m
			}
		case <-timeout:
			ws.t.Fatalf("no %q message within 10s", typ)
		}
	}
}

// closed waits until the server closes the connection and returns the code.
func (ws *wsClient) closed() websocket.StatusCode {
	ws.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-ws.msgs:
			if !ok {
				return websocket.CloseStatus(<-ws.err)
			}
		case <-timeout:
			ws.t.Fatal("connection still open after 10s")
		}
	}
}

func TestTripWebSocket(t *testing.T) {
	e := setup(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	trip := alice.createTrip("Live")
	bob.join(alice, trip.ID)

	aws := alice.dialTrip(trip.ID, alice.token)
	ready := aws.next("ready")
	if ready["user_id"] != alice.user.ID || ready["trip"].(map[string]any)["id"] != trip.ID ||
		len(ready["participants"].([]any)) != 2 {
		t.Fatalf("ready = %v", ready)
	}

	bws := bob.dialTrip(trip.ID, bob.token)
	bws.next("ready")
	for {
		p := aws.next("presence")
		if p["user_id"] == bob.user.ID && p["online"] == true {
			break
		}
	}
	if online := alice.participants(trip.ID); !online[0].Online || !online[1].Online {
		t.Errorf("participants should be online: %+v", online)
	}

	// Locations only while the trip is active.
	bws.send(map[string]any{"type": "location", "lat": 46.5, "lon": 8.5})
	if m := bws.next("error"); m["code"] != "trip_not_active" {
		t.Errorf("error = %v", m)
	}
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusOK)
	if m := bws.next("trip"); m["type"] != "trip" {
		t.Errorf("trip change = %v", m)
	}

	bws.send(map[string]any{"type": "location", "lat": 46.5, "lon": 8.5, "speed_mps": 1.5})
	loc := aws.next("location")
	if loc["user_id"] != bob.user.ID || loc["lat"] != 46.5 || loc["lon"] != 8.5 || loc["speed_mps"] != 1.5 || loc["recorded_at"] == nil {
		t.Errorf("location = %v", loc)
	}
	bws.next("location") // the sender gets it too

	bws.send(map[string]any{"type": "location", "lat": 146.5, "lon": 8.5})
	if m := bws.next("error"); m["code"] != "invalid_location" {
		t.Errorf("error = %v", m)
	}
	bws.send(map[string]any{"type": "ping"})
	bws.next("pong")
	bws.send(map[string]any{"type": "auth", "token": bob.token}) // token refresh is accepted silently
	bws.send(map[string]any{"type": "ping"})
	bws.next("pong")

	// Snapshot for a new connection includes the last position.
	aws2 := alice.dialTrip(trip.ID, alice.token)
	for _, p := range aws2.next("ready")["participants"].([]any) {
		pm := p.(map[string]any)
		if pm["user_id"] == bob.user.ID && pm["last_position"].(map[string]any)["lat"] != 46.5 {
			t.Errorf("bob in snapshot = %v", pm)
		}
	}
	aws2.conn.Close(websocket.StatusNormalClosure, "")

	// Going offline.
	bws.conn.Close(websocket.StatusNormalClosure, "bye")
	for {
		p := aws.next("presence")
		if p["user_id"] == bob.user.ID && p["online"] == false {
			break
		}
	}

	t.Run("authentication", func(t *testing.T) {
		if code := carol.dialTrip(trip.ID, carol.token).closed(); code != trips.CloseNotFound {
			t.Errorf("non-participant: close code %d", code)
		}
		if code := bob.dialTrip(trip.ID, "not-a-token").closed(); code != trips.CloseUnauthorized {
			t.Errorf("bad token: close code %d", code)
		}
		ws := bob.dialTrip(trip.ID, "")
		ws.send(map[string]any{"type": "location", "lat": 1, "lon": 1})
		if code := ws.closed(); code != trips.CloseUnauthorized {
			t.Errorf("no auth message: close code %d", code)
		}
		ws = bob.dialTrip(trip.ID, bob.token)
		ws.next("ready")
		ws.send(map[string]any{"type": "auth", "token": alice.token})
		if code := ws.closed(); code != trips.CloseUnauthorized {
			t.Errorf("switching users: close code %d", code)
		}
		resp := bob.do("GET", "/trips/not-a-uuid/ws", nil)
		if resp.Status != http.StatusNotFound {
			t.Errorf("malformed id: %d", resp.Status)
		}
	})

	t.Run("removed participant is disconnected", func(t *testing.T) {
		ws := bob.dialTrip(trip.ID, bob.token)
		ws.next("ready")
		alice.do("DELETE", "/trips/"+trip.ID+"/participants/"+bob.user.ID, nil).expect(http.StatusNoContent)
		if code := ws.closed(); code != trips.CloseForbidden {
			t.Errorf("close code %d", code)
		}
		aws.next("participants")
	})

	t.Run("deleted trip disconnects everyone", func(t *testing.T) {
		alice.do("DELETE", "/trips/"+trip.ID, nil).expect(http.StatusNoContent)
		if code := aws.closed(); code != trips.CloseNotFound {
			t.Errorf("close code %d", code)
		}
	})
}

func TestTripWebSocketExpiredTokenAndShutdown(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	trip := alice.createTrip("Live")

	// A token about to expire: the server closes with 4001 when it does.
	short := e.signToken(t, alice.user.ID, time.Now().Add(3*time.Second))
	ws := alice.dialTrip(trip.ID, short)
	ws.next("ready")
	if code := ws.closed(); code != trips.CloseUnauthorized {
		t.Errorf("expired token: close code %d", code)
	}

	ws = alice.dialTrip(trip.ID, alice.token)
	ws.next("ready")
	stream := alice.openEvents()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.handler.CloseStreams(ctx); err != nil {
		t.Fatalf("CloseStreams: %v", err)
	}
	if code := ws.closed(); code != websocket.StatusGoingAway {
		t.Errorf("shutdown: close code %d", code)
	}
	if ev := stream.next(); ev.Name != "shutdown" {
		t.Errorf("event stream at shutdown: %q", ev.Name)
	}
	// New connections are refused while shutting down.
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/trips/" + trip.ID + "/ws"
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("dial during shutdown: %v %v", resp, err)
	}
}

func TestLocationRateLimit(t *testing.T) {
	e := setup(t, withRateLimits(server.RateLimits{LocationsPerSecond: 0.001, LocationsBurst: 2}))
	alice := e.register("alice")
	trip := alice.createTrip("Fast")
	alice.do("POST", "/trips/"+trip.ID+"/start", nil).expect(http.StatusOK)
	ws := alice.dialTrip(trip.ID, alice.token)
	ws.next("ready")
	for range 3 {
		ws.send(map[string]any{"type": "location", "lat": 1, "lon": 1})
	}
	if m := ws.next("error"); m["code"] != "rate_limited" {
		t.Errorf("error = %v", m)
	}
}

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

// signToken issues an access token for userID that expires at exp.
func (e *env) signToken(t *testing.T, userID string, exp time.Time) string {
	t.Helper()
	key, err := jwt.ParseECPrivateKeyFromPEM(e.priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer: "travel-docs", Subject: userID,
		IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(exp),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
