// Package billing stores owner-confirmed financial facts independently of AI extraction.
package billing

import (
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

var (
	ErrInvalid     = errors.New("invalid billing request")
	ErrConflict    = errors.New("billing conflict")
	ErrNotFound    = errors.New("billing event not found")
	ErrUnavailable = errors.New("billing unavailable")
)

type Conflict struct{ Reason string }

func (e *Conflict) Error() string     { return "billing conflict: " + e.Reason }
func (e *Conflict) Unwrap() error     { return ErrConflict }
func NewConflict(reason string) error { return &Conflict{Reason: reason} }

type Values struct {
	Kind       string  `json:"kind"`
	OccurredOn string  `json:"occurred_on"`
	Amount     string  `json:"amount"`
	Currency   string  `json:"currency"`
	RefundOfID *string `json:"refund_of_id"`
	State      string  `json:"state"`
}
type Entry struct {
	Provenance     string `json:"provenance"`
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	Version        int64  `json:"version"`
	Values
	Sources   []subscriptions.Source `json:"sources"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}
type CreateInput struct {
	Values
	RequestID          string  `json:"request_id"`
	SourceResultID     *string `json:"source_result_id"`
	SourceEventIndex   *int    `json:"source_event_index"`
	Confirmed          bool    `json:"confirmed"`
	DuplicateConfirmed bool    `json:"duplicate_confirmed"`
	DuplicateToken     *string `json:"duplicate_token"`
}
type UpdateInput struct {
	Values
	ExpectedVersion    int64   `json:"expected_version"`
	Confirmed          bool    `json:"confirmed"`
	DuplicateConfirmed bool    `json:"duplicate_confirmed"`
	DuplicateToken     *string `json:"duplicate_token"`
}
type SourceInput = subscriptions.SourceInput
type Filter struct {
	Kind             string
	State            string
	Month            string
	SourceResultID   *string
	SourceEventIndex *int
	Limit            int
	Offset           int
}
type Page struct {
	Events []Entry `json:"events"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}
type DuplicateFilter struct {
	Kind       string
	OccurredOn string
	Amount     string
	Currency   string
	ExcludeID  string
	Limit      int
	Offset     int
}
type DuplicatePage struct {
	Page
	Token string `json:"token"`
}
type CurrencySummary struct {
	Currency      string `json:"currency"`
	PaymentAmount string `json:"payment_amount"`
	RefundAmount  string `json:"refund_amount"`
	NetAmount     string `json:"net_amount"`
	PaymentCount  int    `json:"payment_count"`
	RefundCount   int    `json:"refund_count"`
}
type Summary struct {
	Month          string            `json:"month"`
	StartDate      string            `json:"start_date"`
	EndDate        string            `json:"end_date"`
	Timezone       string            `json:"timezone"`
	SubscriptionID *string           `json:"subscription_id"`
	Currencies     []CurrencySummary `json:"currencies"`
	InvoiceCount   int               `json:"invoice_count"`
	VoidedCount    int               `json:"voided_count"`
}
