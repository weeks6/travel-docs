// Package trips implements group trips: invitations, participants, live
// location sharing over WebSockets, presence, location history and its
// retention.
//
// Permissions:
//   - the owner can edit, start, end and delete the trip, invite people and
//     remove participants;
//   - participants (members) can see the trip, everyone's positions and the
//     history, share their own location and leave;
//   - everyone else gets 404.
package trips

import (
	"time"

	"github.com/weeks6/travel-docs/internal/apperr"
	"github.com/weeks6/travel-docs/internal/paging"
)

const (
	RoleOwner  = "owner"
	RoleMember = "member"

	StatusPlanned = "planned"
	StatusActive  = "active"
	StatusEnded   = "ended"

	DefaultRetentionDays = 30
	MaxRetentionDays     = 365

	// MaxBatchPoints limits points per POST /trips/{id}/locations.
	MaxBatchPoints = 100
	// MaxPointAge is how old a reported point may be (offline buffering).
	MaxPointAge = 24 * time.Hour
	// MaxClockSkew is how far in the future a point's time may be.
	MaxClockSkew = 2 * time.Minute
	// PresenceTimeout is how long a participant counts as online after the
	// last sign of life (connections send one at least every PresenceInterval).
	PresenceTimeout  = 75 * time.Second
	PresenceInterval = 25 * time.Second
)

var (
	ErrNotFound       = apperr.New(apperr.NotFound, "trip not found")
	errOwnerOnly      = apperr.New(apperr.Forbidden, "only the trip owner can do this")
	errNotActive      = apperr.New(apperr.Conflict, "the trip is not active; locations can be shared only while it is running")
	errInvitationGone = apperr.New(apperr.NotFound, "invitation not found")
)

// Trip is a group trip as seen by one participant.
type Trip struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	Status                string     `json:"status"`
	RouteID               *string    `json:"route_id"`   // only if the viewer may see the route
	RouteName             *string    `json:"route_name"` // likewise
	LocationRetentionDays int        `json:"location_retention_days"`
	OwnerID               string     `json:"owner_id"`
	OwnerName             string     `json:"owner_name"`
	MyRole                string     `json:"my_role"`
	ParticipantCount      int        `json:"participant_count"`
	StartedAt             *time.Time `json:"started_at"`
	EndedAt               *time.Time `json:"ended_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// Position is a reported location.
type Position struct {
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	Ele        *float64  `json:"ele,omitempty"`
	AccuracyM  *float64  `json:"accuracy_m,omitempty"`
	SpeedMps   *float64  `json:"speed_mps,omitempty"`
	HeadingDeg *float64  `json:"heading_deg,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Participant is a trip member with presence and last known position.
type Participant struct {
	UserID       string     `json:"user_id"`
	DisplayName  string     `json:"display_name"`
	Role         string     `json:"role"`
	JoinedAt     time.Time  `json:"joined_at"`
	Online       bool       `json:"online"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	LastPosition *Position  `json:"last_position"`
}

// Invitation to join a trip.
type Invitation struct {
	ID            string     `json:"id"`
	TripID        string     `json:"trip_id"`
	TripName      string     `json:"trip_name"`
	TripStatus    string     `json:"trip_status"`
	InviteeID     string     `json:"invitee_id"`
	InviteeName   string     `json:"invitee_name"`
	InviteeEmail  string     `json:"invitee_email,omitempty"` // only for the trip owner
	InvitedByID   *string    `json:"invited_by_id"`
	InvitedByName *string    `json:"invited_by_name"`
	Status        string     `json:"status"` // pending, accepted, declined, revoked
	CreatedAt     time.Time  `json:"created_at"`
	RespondedAt   *time.Time `json:"responded_at"`
}

// Input creates or updates a trip; nil fields are unchanged. RouteID "" removes the route.
type Input struct {
	Name                  *string `json:"name"`
	Description           *string `json:"description"`
	RouteID               *string `json:"route_id"`
	LocationRetentionDays *int    `json:"location_retention_days"`
}

// ListResult is one page of trips.
type ListResult = paging.Page[Trip]

// Real-time events published on the trip topic and sent to WebSocket clients.
type (
	// LocationEvent: a participant's new position.
	LocationEvent struct {
		Type   string `json:"type"` // "location"
		UserID string `json:"user_id"`
		Position
	}
	// PresenceEvent: a participant came online or went offline.
	PresenceEvent struct {
		Type   string    `json:"type"` // "presence"
		UserID string    `json:"user_id"`
		Online bool      `json:"online"`
		At     time.Time `json:"at"`
	}
	// ChangeEvent: something else changed. Type is "trip" (details or
	// status; reload the trip), "participants" (reload participants),
	// "removed" (UserID left or was removed) or "deleted".
	ChangeEvent struct {
		Type   string `json:"type"`
		UserID string `json:"user_id,omitempty"`
	}
)

// UserEvent is published on a user's topic about their invitations.
type UserEvent struct {
	Type       string     `json:"type"` // "invitation"
	Invitation Invitation `json:"invitation"`
}
