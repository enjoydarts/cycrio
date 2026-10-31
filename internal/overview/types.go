package overview

import (
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

var (
	ErrInvalid     = errors.New("invalid overview request")
	ErrNotFound    = errors.New("overview not found")
	ErrUnavailable = errors.New("overview unavailable")
)

type Window struct {
	AsOf        string    `json:"as_of"`
	WindowEnd   string    `json:"window_end"`
	Timezone    string    `json:"timezone"`
	GeneratedAt time.Time `json:"generated_at"`
}
type Counts struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Ended    int `json:"ended"`
	Unknown  int `json:"unknown"`
	Upcoming int `json:"upcoming"`
	Overdue  int `json:"overdue"`
	Undated  int `json:"undated"`
	Later    int `json:"later"`
}
type Upcoming struct {
	ID         string `json:"id"`
	Provenance string `json:"provenance"`
	subscriptions.Values
}
type Connection struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	State           string     `json:"state"`
	AutomationState string     `json:"automation_state"`
	Ready           bool       `json:"ready"`
	BlockedReasons  []string   `json:"blocked_reasons"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	NextCheckAt     *time.Time `json:"next_check_at"`
}
type Report struct {
	Window
	Subscriptions     Counts       `json:"subscriptions"`
	AttentionMessages int          `json:"attention_messages"`
	Upcoming          []Upcoming   `json:"upcoming"`
	Connections       []Connection `json:"connections"`
}
