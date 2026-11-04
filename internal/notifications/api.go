package notifications

import (
	"errors"
	"time"
)

var (
	ErrConflict    = errors.New("notification conflict")
	ErrNotFound    = errors.New("notification not found")
	ErrNotReady    = errors.New("notification delivery unavailable")
	ErrUnavailable = errors.New("notifications unavailable")
)

type SettingsInput struct {
	Preferences
	ExpectedVersion int64 `json:"expected_version"`
	Confirmed       bool  `json:"confirmed"`
}
type EmailAvailability struct {
	Available    bool   `json:"available"`
	Reason       string `json:"reason"`
	ProfileReady bool   `json:"profile_ready"`
	SenderReady  bool   `json:"sender_ready"`
}
type SettingsPage struct {
	Preferences Preferences       `json:"preferences"`
	Email       EmailAvailability `json:"email"`
	AsOf        string            `json:"as_of"`
	Timezone    string            `json:"timezone"`
}
type Item struct {
	ID           string     `json:"id"`
	Facts        Candidate  `json:"facts"`
	MatchBasis   string     `json:"match_basis,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ReadAt       *time.Time `json:"read_at"`
	SupersededAt *time.Time `json:"superseded_at"`
}
type Filter struct {
	Read          string
	Limit, Offset int
}
type Page struct {
	Notifications []Item `json:"notifications"`
	Total         int    `json:"total"`
	UnreadCount   int    `json:"unread_count"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
	AsOf          string `json:"as_of"`
	Timezone      string `json:"timezone"`
}
type Delivery struct {
	ID          string    `json:"id"`
	Channel     string    `json:"channel"`
	State       string    `json:"state"`
	Reason      string    `json:"reason"`
	ScheduledAt time.Time `json:"scheduled_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Sent        bool      `json:"sent"`
	Delivered   bool      `json:"delivered"`
	Bounced     bool      `json:"bounced"`
	Complained  bool      `json:"complained"`
	Failed      bool      `json:"failed"`
}
type DeliveriesPage struct {
	Deliveries []Delivery `json:"deliveries"`
}
