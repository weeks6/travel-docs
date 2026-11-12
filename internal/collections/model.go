// Package collections groups routes into collections that can be shared.
//
// Permissions:
//   - viewers (shared viewer, or anyone for public collections) can see the
//     collection and the routes their owners put in it;
//   - editors can rename it and add or remove routes;
//   - only the owner can change visibility, manage shares and delete it.
//
// A route can be added by a collection owner/editor only if they own the
// route or the route is public. Adding your own route makes it visible to
// everyone who can see the collection.
package collections

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
)

var (
	ErrNotFound  = apperr.New(apperr.NotFound, "collection not found")
	errReadOnly  = apperr.New(apperr.Forbidden, "you can view this collection but not edit it")
	errOwnerOnly = apperr.New(apperr.Forbidden, "only the collection owner can do this")
)

type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	OwnerID     string    `json:"owner_id"`
	OwnerName   string    `json:"owner_name"`
	MyRole      string    `json:"my_role"`
	RouteCount  int       `json:"route_count"` // routes visible to the requester
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Input is used to create or update a collection; nil fields are unchanged.
type Input struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

func (in *Input) validate(creating bool) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > 200 {
			return apperr.New(apperr.Invalid, "name must be 1–200 characters")
		}
		in.Name = &n
	} else if creating {
		return apperr.New(apperr.Invalid, "name is required")
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 5000 {
		return apperr.New(apperr.Invalid, "description must be at most 5000 characters")
	}
	if in.Visibility != nil && *in.Visibility != "private" && *in.Visibility != "public" {
		return apperr.New(apperr.Invalid, "visibility must be private or public")
	}
	return nil
}

// ListResult is one page of collections.
type ListResult = paging.Page[Collection]

var errRouteNotFound = apperr.New(apperr.NotFound, "route not found")
