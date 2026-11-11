package apitest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestRegisterAndLogin(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	if alice.user.Email != "alice@example.com" || alice.user.DisplayName != "alice" {
		t.Errorf("user = %+v", alice.user)
	}
	if alice.token == "" || alice.refresh == "" {
		t.Fatal("register must return an access and a refresh token")
	}

	var me userJSON
	alice.do("GET", "/auth/me", nil).expect(http.StatusOK).decode(&me)
	if me.ID != alice.user.ID {
		t.Errorf("me = %+v", me)
	}

	t.Run("duplicate email is case-insensitive", func(t *testing.T) {
		resp := e.anon().do("POST", "/auth/register", map[string]string{"email": "ALICE@example.com", "password": testPassword})
		resp.expect(http.StatusConflict)
	})

	t.Run("validation", func(t *testing.T) {
		cases := []struct {
			body map[string]string
			want string
		}{
			{map[string]string{"email": "nope", "password": testPassword}, "email"},
			{map[string]string{"email": "Bob <b@example.com>", "password": testPassword}, "email"},
			{map[string]string{"email": "b@example.com", "password": "short"}, "at least 8"},
			{map[string]string{"email": "b@example.com", "password": strings.Repeat("x", 73)}, "at most 72"},
			{map[string]string{"email": "b@example.com", "password": testPassword, "display_name": "   "}, "display_name"},
		}
		for _, tc := range cases {
			resp := e.anon().do("POST", "/auth/register", tc.body).expect(http.StatusBadRequest)
			if !strings.Contains(resp.errorMsg(), tc.want) {
				t.Errorf("%v: error %q, want %q", tc.body, resp.errorMsg(), tc.want)
			}
		}
		e.anon().do("POST", "/auth/register", map[string]any{"email": "b@example.com", "password": testPassword, "admin": true}).
			expect(http.StatusBadRequest)
	})

	t.Run("login", func(t *testing.T) {
		s := e.anon().login(" Alice@Example.com ", testPassword)
		if s.User.ID != alice.user.ID || s.TokenType != "Bearer" || s.ExpiresIn < 800 ||
			s.RefreshToken == "" || s.RefreshExpiresIn < 29*24*3600 {
			t.Errorf("session = %+v", s)
		}

		bad := e.anon().do("POST", "/auth/login", map[string]string{"email": "alice@example.com", "password": "wrong password"})
		bad.expect(http.StatusUnauthorized)
		unknown := e.anon().do("POST", "/auth/login", map[string]string{"email": "nobody@example.com", "password": testPassword})
		unknown.expect(http.StatusUnauthorized)
		if bad.errorMsg() != unknown.errorMsg() {
			t.Errorf("errors reveal whether the email exists: %q vs %q", bad.errorMsg(), unknown.errorMsg())
		}
		if !strings.Contains(actions(alice.events("")), "user.login_failed") {
			t.Error("failed login should appear in the owner's audit log")
		}
	})
}

func TestAccessTokenValidation(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")

	check := func(name, header string, want int) {
		t.Helper()
		c := e.anon()
		resp := c.do("GET", "/auth/me", nil, "Authorization: "+header)
		if resp.Status != want {
			t.Errorf("%s: status %d, want %d (%s)", name, resp.Status, want, resp.Body)
		}
	}
	check("valid", "Bearer "+alice.token, http.StatusOK)
	check("lowercase scheme", "bearer "+alice.token, http.StatusOK)
	check("missing scheme", alice.token, http.StatusUnauthorized)
	check("garbage", "Bearer abc.def.ghi", http.StatusUnauthorized)
	check("tampered", "Bearer "+alice.token[:len(alice.token)-4]+"AAAA", http.StatusUnauthorized)

	// alg=none must be rejected.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer: "travel-docs", Subject: alice.user.ID,
		IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	check("alg none", "Bearer "+s, http.StatusUnauthorized)

	// Correctly signed but expired.
	key, _ := jwt.ParseECPrivateKeyFromPEM(e.priv)
	expired := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer: "travel-docs", Subject: alice.user.ID,
		IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	})
	s, _ = expired.SignedString(key)
	check("expired", "Bearer "+s, http.StatusUnauthorized)

	// Endpoints that need a user return 401 for anonymous requests.
	e.anon().do("GET", "/auth/me", nil).expect(http.StatusUnauthorized)
	e.anon().uploadRaw("a.gpx", []byte(sampleGPX), nil).expect(http.StatusUnauthorized)
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	first := alice.refresh

	alice.refreshSession().expect(http.StatusOK)
	if alice.refresh == first || alice.refresh == "" {
		t.Fatal("refresh must rotate the token")
	}
	alice.do("GET", "/auth/me", nil).expect(http.StatusOK)

	// Replaying the first token right away is treated as a race (two tabs):
	// rejected, but the session survives.
	thief := e.anon()
	thief.refreshWith(first).expect(http.StatusUnauthorized)
	alice.refreshSession().expect(http.StatusOK)

	// After the grace period, a replayed token ends the whole session.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE refresh_tokens SET revoked_at = revoked_at - interval '1 minute' WHERE revoked_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	thief.refreshWith(first).expect(http.StatusUnauthorized)
	alice.refreshSession().expect(http.StatusUnauthorized)
	if !strings.Contains(actions(alice.events("")), "user.refresh_token_reuse") {
		t.Error("token reuse should be audited")
	}

	// A separate login (another device) is unaffected.
	other := e.anon()
	other.login("alice@example.com", testPassword)
	other.refreshSession().expect(http.StatusOK)

	// Missing, empty or unknown tokens.
	e.anon().do("POST", "/auth/refresh", nil).expect(http.StatusUnauthorized)
	e.anon().refreshWith("").expect(http.StatusUnauthorized)
	e.anon().refreshWith("not-a-token").expect(http.StatusUnauthorized)
	e.anon().do("POST", "/auth/refresh", map[string]string{"token": "x"}).expect(http.StatusBadRequest)
}

func TestLogout(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	laptop := e.anon()
	laptop.login("alice@example.com", testPassword)

	alice.do("POST", "/auth/logout", map[string]string{"refresh_token": alice.refresh}).expect(http.StatusNoContent)
	alice.refreshSession().expect(http.StatusUnauthorized)
	laptop.refreshSession().expect(http.StatusOK)
	// Logging out twice, or without a token, is fine.
	alice.do("POST", "/auth/logout", map[string]string{"refresh_token": alice.refresh}).expect(http.StatusNoContent)
	e.anon().do("POST", "/auth/logout", nil).expect(http.StatusNoContent)

	// Logging out everywhere also invalidates existing access tokens.
	time.Sleep(1100 * time.Millisecond) // token iat has 1s precision
	phone := e.anon()
	phone.login("alice@example.com", testPassword)
	time.Sleep(1100 * time.Millisecond)
	laptop.do("POST", "/auth/logout-all", nil).expect(http.StatusNoContent)
	phone.do("GET", "/auth/me", nil).expect(http.StatusUnauthorized)
	phone.refreshSession().expect(http.StatusUnauthorized)
}

func TestAccountManagement(t *testing.T) {
	e := setup(t)
	alice := e.register("alice")
	e.register("bob")

	var u userJSON
	alice.do("PATCH", "/auth/me", map[string]string{"display_name": "  Alice A. "}).expect(http.StatusOK).decode(&u)
	if u.DisplayName != "Alice A." {
		t.Errorf("display name = %q", u.DisplayName)
	}

	alice.do("PATCH", "/auth/me", map[string]string{"email": "alice2@example.com"}).expect(http.StatusForbidden)
	alice.do("PATCH", "/auth/me", map[string]string{"email": "bob@example.com", "current_password": testPassword}).
		expect(http.StatusConflict)
	alice.do("PATCH", "/auth/me", map[string]string{"email": "Alice2@example.com", "current_password": testPassword}).
		expect(http.StatusOK).decode(&u)
	if u.Email != "alice2@example.com" {
		t.Errorf("email = %q", u.Email)
	}

	t.Run("change password", func(t *testing.T) {
		time.Sleep(1100 * time.Millisecond)
		other := e.anon()
		other.login("alice2@example.com", testPassword)

		alice.do("PUT", "/auth/me/password", map[string]string{"current_password": "wrong", "new_password": "new password 1"}).
			expect(http.StatusForbidden)
		alice.do("PUT", "/auth/me/password", map[string]string{"current_password": testPassword, "new_password": "short"}).
			expect(http.StatusBadRequest)

		time.Sleep(1100 * time.Millisecond)
		alice.useSession(alice.do("PUT", "/auth/me/password",
			map[string]string{"current_password": testPassword, "new_password": "new password 1"}).expect(http.StatusOK))
		alice.do("GET", "/auth/me", nil).expect(http.StatusOK)
		alice.refreshSession().expect(http.StatusOK)
		other.refreshSession().expect(http.StatusUnauthorized)

		e.anon().do("POST", "/auth/login", map[string]string{"email": "alice2@example.com", "password": testPassword}).
			expect(http.StatusUnauthorized)
		e.anon().do("POST", "/auth/login", map[string]string{"email": "alice2@example.com", "password": "new password 1"}).
			expect(http.StatusOK)
	})

	t.Run("delete account", func(t *testing.T) {
		route := alice.upload("mine", sampleGPX)
		alice.do("DELETE", "/auth/me", map[string]string{"password": "wrong"}).expect(http.StatusForbidden)
		alice.do("DELETE", "/auth/me", map[string]string{"password": "new password 1"}).expect(http.StatusNoContent)
		alice.do("GET", "/auth/me", nil).expect(http.StatusUnauthorized)

		var n int
		e.pool.QueryRow(context.Background(), `SELECT count(*) FROM routes WHERE id = $1`, route.ID).Scan(&n)
		if n != 0 {
			t.Error("deleting an account must delete its routes")
		}
	})
}

func TestRoutesRequireOwner(t *testing.T) {
	e := setup(t)
	_, err := e.pool.Exec(context.Background(), `
		INSERT INTO routes (id, name, original_filename, distance_m, ascent_m, descent_m, point_count, geom, elevation_profile)
		VALUES ('01900000-0000-7000-8000-000000000001', 'No owner', 'x.gpx', 1000, 0, 0, 2,
			ST_GeomFromText('MULTILINESTRING Z((8 46 0, 8 46.01 0))', 4326), '[]')`)
	if err == nil {
		t.Fatal("a route without an owner must be rejected")
	}
}
