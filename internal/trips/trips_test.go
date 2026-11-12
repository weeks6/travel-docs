package trips

import (
	"testing"
	"time"

	"github.com/weeks6/travel-docs/internal/apperr"
)

func TestInputValidate(t *testing.T) {
	now := time.Date(2026, 11, 12, 23, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	num := func(n int) *int { return &n }

	in := Input{Name: str("  Hike "), Date: str(" 2026-11-13 "), DurationS: num(60)}
	if err := in.validate(now); err != nil || *in.Name != "Hike" || *in.Date != "2026-11-13" {
		t.Errorf("valid input: %v, %+v", err, in) // tomorrow is fine (time zones)
	}
	for name, bad := range map[string]Input{
		"future":        {Date: str("2026-11-15")},
		"format":        {Date: str("12.11.2026")},
		"too old":       {Date: str("1850-01-01")},
		"zero duration": {DurationS: num(0)},
		"too long":      {DurationS: num(MaxDurationDays*86400 + 1)},
	} {
		if err := bad.validate(now); apperr.KindOf(err) != apperr.Invalid {
			t.Errorf("%s: err = %v, want Invalid", name, err)
		}
	}
}
