// Package trips is a personal log of trips: a route you went on (one you
// can see, or a GPX file uploaded for the trip), the date and how long it
// took. Trips are private to their owner; everyone else gets 404.
//
// When the route's GPX file has timestamps, the date and duration default
// to the recorded ones. A trip keeps the route's distance and ascent from
// when it was logged, and survives the route being deleted.
package trips

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
)

const (
	dateLayout      = "2006-01-02"
	maxNameLength   = 200
	maxNotesLength  = 5000
	MaxDurationDays = 60
)

var (
	ErrNotFound      = apperr.New(apperr.NotFound, "trip not found")
	errRouteGone     = apperr.New(apperr.NotFound, "route not found")
	errNoDate        = apperr.New(apperr.Invalid, "date is required: the route has no recorded start time")
	errNoDuration    = apperr.New(apperr.Invalid, "duration_s is required: the route has no recorded duration")
	errRouteChange   = apperr.New(apperr.Invalid, "a trip's route can't be changed; log a new trip instead")
	errRouteRequired = apperr.New(apperr.Invalid,
		`choose a route ("route_id") or upload a GPX file (multipart/form-data, field "file")`)
	errNameEmpty = apperr.New(apperr.Invalid, "name must not be empty")
)

// Trip is one logged trip.
type Trip struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Notes     string  `json:"notes"`
	Date      string  `json:"date"` // YYYY-MM-DD
	DurationS int     `json:"duration_s"`
	DistanceM float64 `json:"distance_m"` // the route's, when the trip was logged
	AscentM   float64 `json:"ascent_m"`
	// RouteID and RouteName are null when the route was deleted or is no
	// longer visible to the owner.
	RouteID   *string   `json:"route_id"`
	RouteName *string   `json:"route_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListResult is one page of trips, newest date first.
type ListResult = paging.Page[Trip]

// Stats sums up trips.
type Stats struct {
	Trips     int     `json:"trips"`
	DurationS int64   `json:"duration_s"`
	DistanceM float64 `json:"distance_m"`
	AscentM   float64 `json:"ascent_m"`
	FirstDate *string `json:"first_date"`
	LastDate  *string `json:"last_date"`
}

// Input creates or changes a trip; nil fields are unchanged (or, when
// creating, default to the route's name, recorded date and duration).
type Input struct {
	RouteID   *string `json:"route_id"` // required when logging an existing route
	Name      *string `json:"name"`
	Notes     *string `json:"notes"`
	Date      *string `json:"date"` // YYYY-MM-DD
	DurationS *int    `json:"duration_s"`
}

// validate checks and normalizes the given fields.
func (in *Input) validate(now time.Time) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(n) > maxNameLength {
			return apperr.Newf(apperr.Invalid, "name must be at most %d characters", maxNameLength)
		}
		in.Name = &n
	}
	if in.Notes != nil && utf8.RuneCountInString(*in.Notes) > maxNotesLength {
		return apperr.Newf(apperr.Invalid, "notes must be at most %d characters", maxNotesLength)
	}
	if in.Date != nil {
		d, err := time.Parse(dateLayout, strings.TrimSpace(*in.Date))
		if err != nil {
			return apperr.New(apperr.Invalid, "date must be a date such as 2026-05-01")
		}
		// A day of slack for time zones ahead of UTC.
		if d.After(now.UTC().AddDate(0, 0, 1)) || d.Year() < 1900 {
			return apperr.New(apperr.Invalid, "date must not be in the future")
		}
		s := d.Format(dateLayout)
		in.Date = &s
	}
	if in.DurationS != nil && (*in.DurationS < 1 || *in.DurationS > MaxDurationDays*86400) {
		return apperr.Newf(apperr.Invalid, "duration_s must be between 1 second and %d days", MaxDurationDays)
	}
	return nil
}
