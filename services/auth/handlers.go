package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/audit"
)

// Seconds within which a just-rotated refresh token may be presented again
// without being treated as theft (e.g. two browser tabs refreshing at once).
const reuseGracePeriod = 10 * time.Second

// User is the public view of an account.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// sessionResponse is returned by register, login, refresh and password
// change. Clients send access_token as "Authorization: Bearer ..." and keep
// refresh_token to call /auth/refresh and /auth/logout.
type sessionResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	User             User   `json:"user"`
}

// refreshRequest is the body of /auth/refresh and /auth/logout.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RegisterHandlers adds the /auth endpoints.
func (s *Service) RegisterHandlers(e *echo.Echo) {
	limit := middleware.BodyLimit(64 << 10)
	g := e.Group("/auth")
	g.POST("/register", s.register, limit)
	g.POST("/login", s.login, limit)
	g.POST("/refresh", s.refresh, limit)
	g.POST("/logout", s.logout, limit)
	g.POST("/logout-all", s.logoutAll)
	g.GET("/me", s.me)
	g.PATCH("/me", s.updateMe, limit)
	g.PUT("/me/password", s.changePassword, limit)
	g.DELETE("/me", s.deleteMe, limit)
}

func (s *Service) register(c *echo.Context) error {
	r := c.Request()
	var req struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return err
	}
	if req.DisplayName == "" {
		req.DisplayName, _, _ = strings.Cut(email, "@")
	}
	name, err := normalizeDisplayName(req.DisplayName)
	if err != nil {
		return err
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		return err
	}

	var user User
	var refresh string
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO users (id, email, password_hash, display_name)
			VALUES ($1, $2, $3, $4)
			RETURNING id::text, email, display_name, created_at`,
			uuid.NewV7().String(), email, hash, name,
		).Scan(&user.ID, &user.Email, &user.DisplayName, &user.CreatedAt)
		if isUniqueViolation(err) {
			return apperr.New(apperr.Conflict, "an account with this email already exists")
		}
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, audit.Event{
			ActorID: user.ID, OwnerID: user.ID, Action: "user.register", SubjectType: "user", SubjectID: user.ID,
		}); err != nil {
			return err
		}
		refresh, err = s.createRefreshToken(r.Context(), tx, user.ID, "", r.UserAgent())
		return err
	})
	if err != nil {
		return err
	}
	return s.writeSession(c, http.StatusCreated, user, refresh)
}

func (s *Service) login(c *echo.Context) error {
	r := c.Request()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		checkPassword(dummyHash(), req.Password)
		return errBadCredentials
	}

	var user User
	var hash string
	err = s.pool.QueryRow(r.Context(), `
		SELECT id::text, email, display_name, created_at, password_hash
		FROM users WHERE lower(email) = $1`, email,
	).Scan(&user.ID, &user.Email, &user.DisplayName, &user.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		checkPassword(dummyHash(), req.Password)
		return errBadCredentials
	}
	if err != nil {
		return err
	}

	if !checkPassword(hash, req.Password) {
		// Recorded so the account owner can see failed attempts in their log.
		if err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
			return audit.Record(r.Context(), tx, audit.Event{
				OwnerID: user.ID, Action: "user.login_failed", SubjectType: "user", SubjectID: user.ID,
			})
		}); err != nil {
			return err
		}
		return errBadCredentials
	}

	var refresh string
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		var err error
		if refresh, err = s.createRefreshToken(r.Context(), tx, user.ID, "", r.UserAgent()); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{
			ActorID: user.ID, OwnerID: user.ID, Action: "user.login", SubjectType: "user", SubjectID: user.ID,
		})
	})
	if err != nil {
		return err
	}
	return s.writeSession(c, http.StatusOK, user, refresh)
}

// refresh takes {"refresh_token": "..."}, rotates it and returns a new session.
func (s *Service) refresh(c *echo.Context) error {
	r := c.Request()
	var req refreshRequest
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	if req.RefreshToken == "" {
		return errBadRefreshToken
	}

	var user User
	var newRefresh string
	reused := false
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		var tokenID, familyID string
		var expiresAt time.Time
		var revokedAt *time.Time
		err := tx.QueryRow(r.Context(), `
			SELECT t.id::text, t.family_id::text, t.expires_at, t.revoked_at,
			       u.id::text, u.email, u.display_name, u.created_at
			FROM refresh_tokens t JOIN users u ON u.id = t.user_id
			WHERE t.token_hash = $1
			FOR UPDATE OF t`, hashToken(req.RefreshToken),
		).Scan(&tokenID, &familyID, &expiresAt, &revokedAt, &user.ID, &user.Email, &user.DisplayName, &user.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errBadRefreshToken
		}
		if err != nil {
			return err
		}

		if revokedAt != nil {
			if s.now().Sub(*revokedAt) < reuseGracePeriod {
				return errBadRefreshToken
			}
			// An old token was used again: it was probably stolen. End the
			// whole session. This must commit, so don't return an error here.
			reused = true
			if _, err := tx.Exec(r.Context(),
				`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
				return err
			}
			return audit.Record(r.Context(), tx, audit.Event{
				OwnerID: user.ID, Action: "user.refresh_token_reuse", SubjectType: "user", SubjectID: user.ID,
			})
		}
		if !expiresAt.After(s.now()) {
			return errBadRefreshToken
		}

		if _, err := tx.Exec(r.Context(), `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1`, tokenID); err != nil {
			return err
		}
		newRefresh, err = s.createRefreshToken(r.Context(), tx, user.ID, familyID, r.UserAgent())
		return err
	})
	if err == nil && reused {
		err = errBadRefreshToken
	}
	if err != nil {
		return err
	}
	return s.writeSession(c, http.StatusOK, user, newRefresh)
}

// logout takes {"refresh_token": "..."} and ends that session. The client
// should also discard its access token, which expires on its own.
func (s *Service) logout(c *echo.Context) error {
	r := c.Request()
	var req refreshRequest
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	if req.RefreshToken == "" {
		return c.NoContent(http.StatusNoContent)
	}
	err := pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		var userID string
		err := tx.QueryRow(r.Context(), `
			UPDATE refresh_tokens SET revoked_at = now()
			WHERE family_id = (SELECT family_id FROM refresh_tokens WHERE token_hash = $1)
			  AND revoked_at IS NULL
			RETURNING user_id::text`, hashToken(req.RefreshToken)).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already logged out
		}
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{
			ActorID: userID, OwnerID: userID, Action: "user.logout", SubjectType: "user", SubjectID: userID,
		})
	})
	if err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// logoutAll ends every session of the current user, including access tokens.
func (s *Service) logoutAll(c *echo.Context) error {
	r := c.Request()
	userID, err := RequireUser(r.Context())
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		if err := revokeAllSessions(r.Context(), tx, userID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{
			ActorID: userID, OwnerID: userID, Action: "user.logout_all", SubjectType: "user", SubjectID: userID,
		})
	})
	if err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Service) me(c *echo.Context) error {
	r := c.Request()
	userID, err := RequireUser(r.Context())
	if err != nil {
		return err
	}
	user, _, err := s.loadUser(r.Context(), s.pool, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, user)
}

// updateMe changes the display name and/or email. Changing the email requires
// the current password.
func (s *Service) updateMe(c *echo.Context) error {
	r := c.Request()
	userID, err := RequireUser(r.Context())
	if err != nil {
		return err
	}
	var req struct {
		DisplayName     *string `json:"display_name"`
		Email           *string `json:"email"`
		CurrentPassword string  `json:"current_password"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}

	var user User
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		var hash string
		var err error
		if user, hash, err = s.loadUser(r.Context(), tx, userID, "FOR UPDATE"); err != nil {
			return err
		}
		var changed []string
		if req.DisplayName != nil {
			name, err := normalizeDisplayName(*req.DisplayName)
			if err != nil {
				return err
			}
			if name != user.DisplayName {
				user.DisplayName = name
				changed = append(changed, "display_name")
			}
		}
		if req.Email != nil {
			email, err := normalizeEmail(*req.Email)
			if err != nil {
				return err
			}
			if email != user.Email {
				if !checkPassword(hash, req.CurrentPassword) {
					return apperr.New(apperr.Forbidden, "current_password is incorrect")
				}
				user.Email = email
				changed = append(changed, "email")
			}
		}
		if len(changed) == 0 {
			return nil
		}
		_, err = tx.Exec(r.Context(),
			`UPDATE users SET display_name = $2, email = $3, updated_at = now() WHERE id = $1`,
			userID, user.DisplayName, user.Email)
		if isUniqueViolation(err) {
			return apperr.New(apperr.Conflict, "an account with this email already exists")
		}
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, audit.Event{
			ActorID: userID, OwnerID: userID, Action: "user.update", SubjectType: "user", SubjectID: userID,
			Details: map[string]any{"fields": changed},
		})
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, user)
}

// changePassword sets a new password, ends all other sessions and returns a
// fresh session for the caller.
func (s *Service) changePassword(c *echo.Context) error {
	r := c.Request()
	userID, err := RequireUser(r.Context())
	if err != nil {
		return err
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	newHash, err := hashPassword(req.NewPassword)
	if err != nil {
		return err
	}

	var user User
	var refresh string
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		var hash string
		var err error
		if user, hash, err = s.loadUser(r.Context(), tx, userID, "FOR UPDATE"); err != nil {
			return err
		}
		if !checkPassword(hash, req.CurrentPassword) {
			return apperr.New(apperr.Forbidden, "current_password is incorrect")
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, userID, newHash); err != nil {
			return err
		}
		if err := revokeAllSessions(r.Context(), tx, userID); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, audit.Event{
			ActorID: userID, OwnerID: userID, Action: "user.password_change", SubjectType: "user", SubjectID: userID,
		}); err != nil {
			return err
		}
		refresh, err = s.createRefreshToken(r.Context(), tx, userID, "", r.UserAgent())
		return err
	})
	if err != nil {
		return err
	}
	return s.writeSession(c, http.StatusOK, user, refresh)
}

// deleteMe permanently deletes the account and everything it owns.
func (s *Service) deleteMe(c *echo.Context) error {
	r := c.Request()
	userID, err := RequireUser(r.Context())
	if err != nil {
		return err
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return err
	}
	err = pgx.BeginFunc(r.Context(), s.pool, func(tx pgx.Tx) error {
		_, hash, err := s.loadUser(r.Context(), tx, userID, "FOR UPDATE")
		if err != nil {
			return err
		}
		if !checkPassword(hash, req.Password) {
			return apperr.New(apperr.Forbidden, "password is incorrect")
		}
		// Routes, collections, shares, sessions and audit events owned by the
		// user are removed by ON DELETE CASCADE.
		_, err = tx.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, userID)
		return err
	})
	if err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) loadUser(ctx context.Context, q querier, userID string, lock ...string) (User, string, error) {
	var u User
	var hash string
	sql := `SELECT id::text, email, display_name, created_at, password_hash FROM users WHERE id = $1`
	if len(lock) > 0 {
		sql += " " + lock[0]
	}
	err := q.QueryRow(ctx, sql, userID).Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, "", ErrUnauthenticated
	}
	return u, hash, err
}

func (s *Service) writeSession(c *echo.Context, status int, user User, refresh string) error {
	access, exp, err := s.issueAccessToken(user.ID)
	if err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.JSON(status, sessionResponse{
		AccessToken:      access,
		TokenType:        "Bearer",
		ExpiresIn:        int(time.Until(exp).Seconds()),
		RefreshToken:     refresh,
		RefreshExpiresIn: int(s.cfg.RefreshTTL.Seconds()),
		User:             user,
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
