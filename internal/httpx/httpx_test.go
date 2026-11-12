package httpx

import (
	"net/url"
	"testing"

	"github.com/weeks6/travel-docs/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
)

func TestIfMatchVersion(t *testing.T) {
	for header, want := range map[string]int{"": 0, "*": 0, `"3"`: 3, `W/"12"`: 12, "7": 7} {
		got, err := IfMatchVersion(header)
		if err != nil || got != want {
			t.Errorf("If-Match %q = %d, %v; want %d", header, got, err, want)
		}
	}
	for _, bad := range []string{`"abc"`, `"0"`, `"-1"`} {
		if _, err := IfMatchVersion(bad); apperr.KindOf(err) != apperr.Invalid {
			t.Errorf("If-Match %q: err = %v, want Invalid", bad, err)
		}
	}
}

func TestParsePage(t *testing.T) {
	for q, want := range map[string]paging.Params{"": {Page: 1, PageSize: 20}, "page=3&page_size=100": {Page: 3, PageSize: 100}} {
		v, _ := url.ParseQuery(q)
		got, err := ParsePage(v)
		if err != nil || got != want {
			t.Errorf("%q = %v, %v; want %v", q, got, err, want)
		}
	}
	for _, q := range []string{"page=0", "page=x", "page_size=0", "page_size=101", "page_size=x"} {
		v, _ := url.ParseQuery(q)
		if _, err := ParsePage(v); apperr.KindOf(err) != apperr.Invalid {
			t.Errorf("%q: err = %v, want Invalid", q, err)
		}
	}
}
