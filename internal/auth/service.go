// Package auth implements accounts, login sessions and request authentication.
//
// Sessions use two tokens:
//   - a short-lived ES256 JWT access token, sent as "Authorization: Bearer ...";
//   - a long-lived opaque refresh token, returned in the response body and
//     sent back in the body of /auth/refresh and /auth/logout; stored hashed
//     in the database and rotated on every use.
//
// No cookies are used.
package auth

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/textutil"
)

// Config configures the auth service.
type Config struct {
	PrivateKeyPEM []byte
	PublicKeyPEM  []byte
	AccessTTL     time.Duration // default 15 minutes
	RefreshTTL    time.Duration // default 30 days
}

// Service issues and verifies sessions.
type Service struct {
	pool      *pgxpool.Pool
	cfg       Config
	signKey   *ecdsa.PrivateKey
	verifyKey *ecdsa.PublicKey
	now       func() time.Time
}

func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	if cfg.AccessTTL == 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL == 0 {
		cfg.RefreshTTL = 30 * 24 * time.Hour
	}
	signKey, verifyKey, err := parseKeys(cfg.PrivateKeyPEM, cfg.PublicKeyPEM)
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, cfg: cfg, signKey: signKey, verifyKey: verifyKey, now: time.Now}, nil
}

var (
	ErrUnauthenticated = apperr.New(apperr.Unauthorized, "authentication required")
	errBadAccessToken  = apperr.New(apperr.Unauthorized, "invalid or expired access token")
	errBadCredentials  = apperr.New(apperr.Unauthorized, "invalid email or password")
	errBadRefreshToken = apperr.New(apperr.Unauthorized, "invalid or expired session")
)

type ctxKey struct{}

// Middleware authenticates requests that carry a bearer token. Requests
// without one continue anonymously; requests with an invalid, expired or
// revoked token are rejected with 401 so the client knows to refresh.
func (s *Service) Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		header := c.Request().Header.Get(echo.HeaderAuthorization)
		if header == "" {
			return next(c)
		}
		scheme, raw, ok := strings.Cut(header, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(raw) == "" {
			return apperr.New(apperr.Unauthorized, "Authorization header must be \"Bearer <token>\"")
		}
		userID, expires, err := s.Authenticate(c.Request().Context(), strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		ctx := WithUser(c.Request().Context(), userID)
		ctx = context.WithValue(ctx, expiryKey{}, expires)
		c.SetRequest(c.Request().WithContext(ctx))
		return next(c)
	}
}

// Authenticate verifies an access token and returns its user and expiry.
// Used directly by connections that can't send an Authorization header
// (WebSockets).
func (s *Service) Authenticate(ctx context.Context, token string) (userID string, expires time.Time, err error) {
	claims, err := s.parseAccessToken(token)
	if err != nil {
		return "", time.Time{}, errBadAccessToken
	}
	// One primary-key lookup per request lets deleted accounts, password
	// changes and "log out everywhere" take effect immediately.
	var validAfter time.Time
	err = s.pool.QueryRow(ctx, `SELECT tokens_valid_after FROM users WHERE id = $1`, claims.Subject).Scan(&validAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, errBadAccessToken
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("load user: %w", err)
	}
	if claims.IssuedAt.Time.Before(validAfter) {
		return "", time.Time{}, errBadAccessToken
	}
	return claims.Subject, claims.ExpiresAt.Time, nil
}

type expiryKey struct{}

// TokenExpiry returns when the request's access token expires (zero for
// anonymous requests). Long-lived streams end then, so the client
// reconnects with a fresh token and revoked sessions don't linger.
func TokenExpiry(ctx context.Context) time.Time {
	t, _ := ctx.Value(expiryKey{}).(time.Time)
	return t
}

// UserID returns the authenticated user's ID, or "" for anonymous requests.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// RequireUser returns the authenticated user's ID or an Unauthorized error.
func RequireUser(ctx context.Context) (string, error) {
	if id := UserID(ctx); id != "" {
		return id, nil
	}
	return "", ErrUnauthenticated
}

// WithUser returns a context authenticated as userID. Intended for tests.
func WithUser(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, userID)
}

// createRefreshToken stores a new refresh token. An empty familyID starts a
// new session.
func (s *Service) createRefreshToken(ctx context.Context, tx pgx.Tx, userID, familyID, userAgent string) (string, error) {
	if familyID == "" {
		familyID = uuid.NewV7().String()
	}
	raw, hash := newRefreshToken()
	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.NewV7().String(), userID, familyID, hash, textutil.Truncate(userAgent, 300), s.now().Add(s.cfg.RefreshTTL))
	if err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}
	return raw, nil
}

// revokeAllSessions revokes every refresh token of the user and invalidates
// access tokens issued before now.
func revokeAllSessions(ctx context.Context, tx pgx.Tx, userID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`UPDATE users SET tokens_valid_after = date_trunc('second', now()) WHERE id = $1`, userID)
	return err
}
