package routes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weeks6/travel-docs/routestats"
)

// ErrNotFound is returned when a route does not exist.
var ErrNotFound = errors.New("route not found")

// Route is the stored route metadata (without geometry).
type Route struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	OriginalFilename string     `json:"original_filename"`
	DistanceM        float64    `json:"distance_m"`
	AscentM          float64    `json:"ascent_m"`
	DescentM         float64    `json:"descent_m"`
	MinElevationM    *float64   `json:"min_elevation_m"`
	MaxElevationM    *float64   `json:"max_elevation_m"`
	PointCount       int        `json:"point_count"`
	StartedAt        *time.Time `json:"started_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// NewRoute is everything needed to insert a route.
type NewRoute struct {
	Route
	// Geometry is a GeoJSON MultiLineString in WGS84.
	Geometry json.RawMessage
	Profile  []routestats.ProfilePoint
}

// SortField is a whitelisted column for ordering lists.
type SortField string

const (
	SortCreatedAt SortField = "created_at"
	SortName      SortField = "name"
	SortDistance  SortField = "distance"
	SortAscent    SortField = "ascent"
)

// BBox is a bounding box in WGS84 degrees.
type BBox struct {
	MinLon, MinLat, MaxLon, MaxLat float64
}

// ListParams controls pagination, filtering and sorting.
type ListParams struct {
	Page     int // 1-based
	PageSize int

	Name         string // case-insensitive substring match
	MinDistanceM *float64
	MaxDistanceM *float64
	MinAscentM   *float64
	MaxAscentM   *float64
	BBox         *BBox // routes that intersect this box

	Sort SortField
	Desc bool
}

// ListResult is one page of routes.
type ListResult struct {
	Items      []Route `json:"items"`
	Page       int     `json:"page"`
	PageSize   int     `json:"page_size"`
	Total      int     `json:"total"`
	TotalPages int     `json:"total_pages"`
}

// Store persists routes.
type Store interface {
	Create(ctx context.Context, r NewRoute) (Route, error)
	Get(ctx context.Context, id string) (Route, error)
	List(ctx context.Context, p ListParams) (ListResult, error)
	// Geometry returns the route geometry as a GeoJSON geometry object.
	Geometry(ctx context.Context, id string) (json.RawMessage, error)
	ElevationProfile(ctx context.Context, id string) ([]routestats.ProfilePoint, error)
	Delete(ctx context.Context, id string) error
}
