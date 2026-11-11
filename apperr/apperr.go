// Package apperr defines application errors that map to HTTP status codes
// without tying the storage layer to net/http.
package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	Internal Kind = iota
	Invalid
	Unauthorized
	Forbidden
	NotFound
	Conflict
	PreconditionFailed
	Unprocessable
)

type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func New(kind Kind, msg string) *Error { return &Error{Kind: kind, Msg: msg} }

func Newf(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf returns the Kind of err, or Internal for errors that are not *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Internal
}
