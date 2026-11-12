package routes

import (
	"encoding/json"
	"time"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
	"github.com/weeks6/travel-docs/internal/routestats"
)

var (
	ErrNotFound  = apperr.New(apperr.NotFound, "route not found")
	errReadOnly  = apperr.New(apperr.Forbidden, "you can view this route but not edit it")
	errOwnerOnly = apperr.New(apperr.Forbidden, "only the route owner can do this")
)

// Roles a user can have on a route.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

const (
	VisibilityPrivate = "private"
	VisibilityPublic  = "public"
)

// Route is the current state of a route (without geometry).
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
	HasElevation     bool       `json:"has_elevation"`
	PointCount       int        `json:"point_count"`
	StartedAt        *time.Time `json:"started_at"`
	// DurationS is how long the recorded track took, when it has timestamps.
	DurationS  *int   `json:"duration_s"`
	Visibility string `json:"visibility"`
	Version    int    `json:"version"`
	OwnerID    string `json:"owner_id"`
	OwnerName  string `json:"owner_name"`
	// MyRole is the requesting user's access: owner, editor or viewer.
	MyRole    string    `json:"my_role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Content is the versioned part of a route: metadata, geometry and stats.
type Content struct {
	Name             string
	Description      string
	OriginalFilename string
	DistanceM        float64
	AscentM          float64
	DescentM         float64
	MinElevationM    *float64
	MaxElevationM    *float64
	HasElevation     bool
	PointCount       int
	StartedAt        *time.Time
	DurationS        *int
	// Geometry is a GeoJSON MultiLineString with [lon, lat, elevation]
	// coordinates (elevation 0 when HasElevation is false).
	Geometry json.RawMessage
	Profile  []routestats.ProfilePoint
}

// Version is one entry of a route's history.
type Version struct {
	Version       int       `json:"version"`
	Change        string    `json:"change"` // create, metadata, geometry or restore
	RestoredFrom  *int      `json:"restored_from"`
	CreatedByID   *string   `json:"created_by_id"`
	CreatedByName *string   `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	DistanceM     float64   `json:"distance_m"`
	AscentM       float64   `json:"ascent_m"`
	DescentM      float64   `json:"descent_m"`
	PointCount    int       `json:"point_count"`
}

// MetadataUpdate holds optional changes; nil fields stay unchanged.
type MetadataUpdate struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

// Scope selects which routes a list returns.
type Scope string

const (
	ScopeMine   Scope = "mine"   // routes I own
	ScopeShared Scope = "shared" // routes shared directly with me
	ScopePublic Scope = "public" // public routes of everyone
	ScopeAll    Scope = "all"    // everything I can see
)

type SortField string

const (
	SortCreatedAt SortField = "created_at"
	SortUpdatedAt SortField = "updated_at"
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

	Scope        Scope
	CollectionID string // only routes in this collection

	Name         string // case-insensitive substring match
	MinDistanceM *float64
	MaxDistanceM *float64
	MinAscentM   *float64
	MaxAscentM   *float64
	BBox         *BBox // routes that intersect this box
	StartsIn     *BBox // routes that start in this box

	Sort SortField
	Desc bool
}

// ListResult is one page of routes.
type ListResult = paging.Page[Route]

// MapParams selects the routes to show on the map: the list filters
// (paging and sorting are ignored), a required BBox and the map's zoom level.
type MapParams struct {
	ListParams
	Zoom int // 0–22
}

// MapResult is what the map shows in a bounding box: routes grouped by
// where they start.
type MapResult struct {
	Clusters []MapCluster `json:"clusters"`
	// Total is the number of routes in Clusters.
	Total int `json:"total"`
	// Truncated is true when there were more clusters than one response
	// holds (the largest are kept).
	Truncated bool `json:"truncated"`
}

// MapCluster is a group of routes starting close to each other at the
// requested zoom level, or a single route.
type MapCluster struct {
	Lat   float64 `json:"lat"` // center of the start points
	Lon   float64 `json:"lon"`
	Count int     `json:"count"`
	// BBox of the start points: minLon, minLat, maxLon, maxLat. Zooming to it
	// splits the cluster.
	BBox [4]float64 `json:"bbox"`
	// Route is set when Count is 1.
	Route *MapRoute `json:"route,omitempty"`
}

// MapRoute identifies the single route of a cluster.
type MapRoute struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	DistanceM float64 `json:"distance_m"`
}
