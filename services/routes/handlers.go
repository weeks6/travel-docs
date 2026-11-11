// Package routes implements GPX route upload, storage and retrieval.
package routes

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"uuid"

	"github.com/weeks6/travel-docs/gpx"
	"github.com/weeks6/travel-docs/routestats"
)

const (
	// MaxUploadBytes limits the size of an upload request.
	MaxUploadBytes  = 20 << 20
	DefaultPageSize = 20
	MaxPageSize     = 100
	maxNameLength   = 200
)

// Handler serves the /routes API.
type Handler struct {
	Store Store
}

// Register adds the route endpoints to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /routes", h.upload)
	mux.HandleFunc("GET /routes", h.list)
	mux.HandleFunc("GET /routes/{id}", h.get)
	mux.HandleFunc("GET /routes/{id}/geometry", h.geometry)
	mux.HandleFunc("GET /routes/{id}/elevation", h.elevation)
	mux.HandleFunc("DELETE /routes/{id}", h.delete)
}

// upload accepts multipart/form-data with a "file" field (the GPX file) and
// an optional "name" field that overrides the name from the file.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("upload is larger than %d MB", MaxUploadBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "expected multipart/form-data with a \"file\" field")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing \"file\" field")
		return
	}
	defer file.Close()

	parsed, err := gpx.Parse(file)
	if err != nil {
		if gpx.IsValidationError(err) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "could not read upload")
		return
	}

	newRoute, err := BuildRoute(parsed, header.Filename, r.FormValue("name"))
	if err != nil {
		h.internalError(w, "build route", err)
		return
	}

	created, err := h.Store.Create(r.Context(), newRoute)
	if err != nil {
		h.internalError(w, "create route", err)
		return
	}
	w.Header().Set("Location", "/routes/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

// BuildRoute turns a parsed GPX file into a route ready to be stored.
// The name is taken from nameOverride, then the GPX file, then the file name.
func BuildRoute(f *gpx.File, filename, nameOverride string) (NewRoute, error) {
	stats := routestats.Compute(f.Segments)

	lines := make([][][2]float64, 0, len(f.Segments))
	for _, seg := range f.Segments {
		line := make([][2]float64, len(seg))
		for i, p := range seg {
			line[i] = [2]float64{p.Lon, p.Lat}
		}
		lines = append(lines, line)
	}
	geom, err := json.Marshal(map[string]any{"type": "MultiLineString", "coordinates": lines})
	if err != nil {
		return NewRoute{}, err
	}

	filename = filepath.Base(filename)
	name := strings.TrimSpace(nameOverride)
	if name == "" {
		name = f.Name
	}
	if name == "" {
		name = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	if name == "" || name == "." {
		name = "Untitled route"
	}
	name = truncate(name, maxNameLength)

	nr := NewRoute{
		Route: Route{
			ID:               uuid.NewV7().String(),
			Name:             name,
			Description:      f.Description,
			OriginalFilename: filename,
			DistanceM:        stats.DistanceM,
			AscentM:          stats.AscentM,
			DescentM:         stats.DescentM,
			MinElevationM:    stats.MinElevationM,
			MaxElevationM:    stats.MaxElevationM,
			PointCount:       f.PointCount(),
		},
		Geometry: geom,
		Profile:  stats.Profile,
	}
	if t, ok := f.StartTime(); ok {
		t = t.UTC()
		nr.StartedAt = &t
	}
	return nr, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	params, err := ParseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.Store.List(r.Context(), params)
	if err != nil {
		h.internalError(w, "list routes", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ParseListParams reads pagination, filter and sort options from the query string:
//
//	page, page_size, name, min_distance_m, max_distance_m, min_ascent_m,
//	max_ascent_m, bbox=minLon,minLat,maxLon,maxLat,
//	sort=created_at|name|distance|ascent, order=asc|desc
func ParseListParams(r *http.Request) (ListParams, error) {
	q := r.URL.Query()
	p := ListParams{Page: 1, PageSize: DefaultPageSize, Sort: SortCreatedAt, Desc: true}

	var err error
	if p.Page, err = intParam(q.Get("page"), 1); err != nil || p.Page < 1 {
		return p, errors.New("page must be a positive integer")
	}
	if p.PageSize, err = intParam(q.Get("page_size"), DefaultPageSize); err != nil || p.PageSize < 1 || p.PageSize > MaxPageSize {
		return p, fmt.Errorf("page_size must be between 1 and %d", MaxPageSize)
	}

	p.Name = strings.TrimSpace(q.Get("name"))

	for key, dst := range map[string]**float64{
		"min_distance_m": &p.MinDistanceM,
		"max_distance_m": &p.MaxDistanceM,
		"min_ascent_m":   &p.MinAscentM,
		"max_ascent_m":   &p.MaxAscentM,
	} {
		if v := q.Get(key); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return p, fmt.Errorf("%s must be a non-negative number", key)
			}
			*dst = &f
		}
	}

	if v := q.Get("bbox"); v != "" {
		b, err := parseBBox(v)
		if err != nil {
			return p, err
		}
		p.BBox = &b
	}

	if v := q.Get("sort"); v != "" {
		switch s := SortField(v); s {
		case SortCreatedAt, SortName, SortDistance, SortAscent:
			p.Sort = s
		default:
			return p, errors.New("sort must be one of created_at, name, distance, ascent")
		}
	}
	switch q.Get("order") {
	case "", "desc":
		p.Desc = true
	case "asc":
		p.Desc = false
	default:
		return p, errors.New("order must be asc or desc")
	}
	return p, nil
}

func parseBBox(v string) (BBox, error) {
	errBBox := errors.New("bbox must be minLon,minLat,maxLon,maxLat")
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return BBox{}, errBBox
	}
	var n [4]float64
	for i, s := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return BBox{}, errBBox
		}
		n[i] = f
	}
	b := BBox{MinLon: n[0], MinLat: n[1], MaxLon: n[2], MaxLat: n[3]}
	if b.MinLon > b.MaxLon || b.MinLat > b.MaxLat ||
		b.MinLon < -180 || b.MaxLon > 180 || b.MinLat < -90 || b.MaxLat > 90 {
		return BBox{}, errBBox
	}
	return b, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	route, err := h.Store.Get(r.Context(), id)
	if h.storeError(w, "get route", err) {
		return
	}
	writeJSON(w, http.StatusOK, route)
}

// geometry returns the route as a GeoJSON Feature, ready for map libraries.
func (h *Handler) geometry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	route, err := h.Store.Get(r.Context(), id)
	if h.storeError(w, "get route", err) {
		return
	}
	geom, err := h.Store.Geometry(r.Context(), id)
	if h.storeError(w, "get geometry", err) {
		return
	}
	w.Header().Set("Content-Type", "application/geo+json")
	writeJSONBody(w, http.StatusOK, map[string]any{
		"type":     "Feature",
		"geometry": geom,
		"properties": map[string]any{
			"id":         route.ID,
			"name":       route.Name,
			"distance_m": route.DistanceM,
		},
	})
}

func (h *Handler) elevation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	profile, err := h.Store.ElevationProfile(r.Context(), id)
	if h.storeError(w, "get elevation profile", err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": profile})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if h.storeError(w, "delete route", h.Store.Delete(r.Context(), id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathID validates the {id} path value. Malformed IDs are reported as 404
// so they never reach the database.
func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, ErrNotFound.Error())
		return "", false
	}
	return id.String(), true
}

// storeError writes an error response if err is non-nil and reports whether it did.
func (h *Handler) storeError(w http.ResponseWriter, op string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		h.internalError(w, op, err)
	}
	return true
}

func (h *Handler) internalError(w http.ResponseWriter, op string, err error) {
	log.Printf("routes: %s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	writeJSONBody(w, status, v)
}

func writeJSONBody(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("routes: write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func intParam(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
