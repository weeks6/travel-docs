// Package httpx holds request parsing and response types shared by the
// API's handlers: path IDs, pagination and optimistic-concurrency headers.
package httpx

import (
	"net/url"
	"strconv"
	"strings"
	"uuid"

	"github.com/labstack/echo/v5"
	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/auth"
	"github.com/weeks6/travel-docs/internal/paging"
)

// ParsePage reads ?page and ?page_size, defaulting to the first page of
// DefaultPageSize items.
func ParsePage(q url.Values) (paging.Params, error) {
	p := paging.Params{Page: 1, PageSize: paging.DefaultPageSize}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, apperr.New(apperr.Invalid, "page must be a positive integer")
		}
		p.Page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > paging.MaxPageSize {
			return p, apperr.Newf(apperr.Invalid, "page_size must be between 1 and %d", paging.MaxPageSize)
		}
		p.PageSize = n
	}
	return p, nil
}

// PathUUID returns the named path parameter in canonical form. Malformed
// IDs are reported as notFound, so they never reach the database.
func PathUUID(c *echo.Context, name string, notFound error) (string, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return "", notFound
	}
	return id.String(), nil
}

// QueryUUID returns the named query parameter in canonical form, or "" when
// it is absent.
func QueryUUID(q url.Values, name string) (string, error) {
	v := q.Get(name)
	if v == "" {
		return "", nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return "", apperr.Newf(apperr.Invalid, "%s must be a UUID", name)
	}
	return id.String(), nil
}

// ActorAndID returns the logged-in user and the :id path parameter, the
// two things most endpoints start with.
func ActorAndID(c *echo.Context, notFound error) (actor, id string, err error) {
	if actor, err = auth.RequireUser(c.Request().Context()); err != nil {
		return "", "", err
	}
	if id, err = PathUUID(c, "id", notFound); err != nil {
		return "", "", err
	}
	return actor, id, nil
}

// IfMatchVersion returns the version from an If-Match header such as "3"
// (an ETag written by WriteVersioned), or 0 when the header is absent.
func IfMatchVersion(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "*" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(v, "W/"), `"`))
	if err != nil || n < 1 {
		return 0, apperr.New(apperr.Invalid, `If-Match must be a version ETag such as "3"`)
	}
	return n, nil
}

// VersionETag formats a version as an ETag for IfMatchVersion.
func VersionETag(version int) string { return strconv.Quote(strconv.Itoa(version)) }
