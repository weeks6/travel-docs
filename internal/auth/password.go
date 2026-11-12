package auth

import (
	"net/mail"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/weeks6/travel-docs/internal/apperr"
	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 8
	// bcrypt ignores everything after 72 bytes, so longer passwords are rejected.
	maxPasswordBytes  = 72
	maxDisplayNameLen = 100
	maxEmailLen       = 254
)

// BcryptCost can be lowered in tests to speed them up.
var BcryptCost = bcrypt.DefaultCost

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(h), err
}

func validatePassword(p string) error {
	if utf8.RuneCountInString(p) < minPasswordLen {
		return apperr.Newf(apperr.Invalid, "password must be at least %d characters", minPasswordLen)
	}
	if len(p) > maxPasswordBytes {
		return apperr.Newf(apperr.Invalid, "password must be at most %d bytes", maxPasswordBytes)
	}
	return nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is compared against when a login email doesn't exist, so the
// response time doesn't reveal which emails are registered.
var dummyHash = sync.OnceValue(func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), BcryptCost)
	return string(h)
})

// normalizeEmail validates a bare address and lowercases it.
func normalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s || len(s) > maxEmailLen {
		return "", apperr.New(apperr.Invalid, "email must be a valid address like name@example.com")
	}
	return strings.ToLower(s), nil
}

func normalizeDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apperr.New(apperr.Invalid, "display_name must not be empty")
	}
	if utf8.RuneCountInString(s) > maxDisplayNameLen {
		return "", apperr.Newf(apperr.Invalid, "display_name must be at most %d characters", maxDisplayNameLen)
	}
	return s, nil
}
