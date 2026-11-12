package routes

import (
	"strings"
	"testing"

	"github.com/weeks6/travel-docs/internal/apperr"
)

func TestLooksLikeXML(t *testing.T) {
	for in, want := range map[string]bool{
		"<gpx/>": true, "\xef\xbb\xbf  \n<?xml?>": true, "": false, "{\"type\":1}": false, "   ": false,
	} {
		if got := LooksLikeXML([]byte(in)); got != want {
			t.Errorf("LooksLikeXML(%q) = %v", in, got)
		}
	}
}

func TestParseGPXRejectsNonXML(t *testing.T) {
	for _, in := range []string{"", "hello", `{"type":"LineString"}`} {
		if _, err := ParseGPX(strings.NewReader(in)); err != ErrNotXML {
			t.Errorf("ParseGPX(%q) = %v, want ErrNotXML", in, err)
		}
	}
	_, err := ParseGPX(strings.NewReader("\xef\xbb\xbf\n<gpx><trk><trkseg></trkseg></trk></gpx>"))
	if apperr.KindOf(err) != apperr.Unprocessable {
		t.Errorf("empty track: err = %v, want Unprocessable", err)
	}
}

func TestParseNewRouteForm(t *testing.T) {
	f, err := ParseNewRouteForm("  Alps  ", "")
	if err != nil || f.Name != "Alps" || f.Visibility != VisibilityPrivate {
		t.Errorf("got %+v, %v", f, err)
	}
	for _, bad := range [][2]string{{strings.Repeat("x", 201), ""}, {"", "friends"}} {
		if _, err := ParseNewRouteForm(bad[0], bad[1]); apperr.KindOf(err) != apperr.Invalid {
			t.Errorf("%q/%q: err = %v, want Invalid", bad[0][:min(len(bad[0]), 5)], bad[1], err)
		}
	}
}
