// Package textutil has small string helpers shared across packages.
package textutil

// Truncate shortens s to at most n runes (never splitting a character).
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
