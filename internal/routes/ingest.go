package routes

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/gpx"
)

// GPX uploads reach routes two ways: directly (POST /routes, PUT
// /routes/{id}/geometry) and through background import jobs. Both use the
// rules in this file, so a file or form accepted by one is accepted by the
// other.

// ErrNotXML rejects uploads that are obviously not GPX.
var ErrNotXML = apperr.New(apperr.Unprocessable, "invalid GPX: the file is not an XML document")

// sniffLen is how much of a file LooksLikeXML needs to see at most.
const sniffLen = 64 << 10

// LooksLikeXML checks that the data starts with "<" after an optional byte
// order mark and whitespace, to reject obviously wrong files early.
func LooksLikeXML(head []byte) bool {
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	head = bytes.TrimLeft(head, " \t\r\n")
	return len(head) > 0 && head[0] == '<'
}

// ParseGPX parses an uploaded GPX file. Files that are not valid GPX are
// Unprocessable errors carrying the reason; other errors come from reading.
func ParseGPX(r io.Reader) (*gpx.File, error) {
	br := bufio.NewReaderSize(r, sniffLen)
	head, err := br.Peek(sniffLen)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, err
	}
	if !LooksLikeXML(head) {
		return nil, ErrNotXML
	}
	f, err := gpx.Parse(br)
	if gpx.IsValidationError(err) {
		return nil, apperr.New(apperr.Unprocessable, err.Error())
	}
	return f, err
}

// CleanFilename keeps only the base name of an uploaded file ("" if none).
func CleanFilename(name string) string {
	name = filepath.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// NewRouteForm holds the optional form fields of an upload that creates a
// route.
type NewRouteForm struct {
	Name       string // "" = from the file
	Visibility string
}

// ParseNewRouteForm validates the "name" and "visibility" form fields.
func ParseNewRouteForm(name, visibility string) (NewRouteForm, error) {
	f := NewRouteForm{Name: strings.TrimSpace(name), Visibility: visibility}
	if utf8.RuneCountInString(f.Name) > maxNameLength {
		return f, apperr.Newf(apperr.Invalid, "name must be at most %d characters", maxNameLength)
	}
	if f.Visibility == "" {
		f.Visibility = VisibilityPrivate
	}
	return f, validateVisibility(f.Visibility)
}
