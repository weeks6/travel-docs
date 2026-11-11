package auth

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		"a@example.com":             "a@example.com",
		"  Mixed.Case@Example.COM ": "mixed.case@example.com",
	} {
		got, err := normalizeEmail(in)
		if err != nil || got != want {
			t.Errorf("normalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "plain", "a@", "Name <a@example.com>", "a@example.com, b@example.com", strings.Repeat("a", 250) + "@x.com"} {
		if _, err := normalizeEmail(bad); err == nil {
			t.Errorf("normalizeEmail(%q): expected error", bad)
		}
	}
}

func TestPasswordHashing(t *testing.T) {
	BcryptCost = 4
	h, err := hashPassword("long enough pw")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword(h, "long enough pw") || checkPassword(h, "long enough pW") {
		t.Error("checkPassword mismatch")
	}
	if _, err := hashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
	if _, err := hashPassword(strings.Repeat("ü", 40)); err == nil {
		t.Error("password over 72 bytes accepted")
	}
	if err := validatePassword("ünïcödé!"); err != nil {
		t.Errorf("8-character unicode password rejected: %v", err)
	}
}
