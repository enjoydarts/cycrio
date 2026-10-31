// Package subscriptions stores owner-confirmed contracts separately from AI results.
package subscriptions

import (
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

var (
	ErrInvalid     = errors.New("invalid subscription request")
	ErrConflict    = errors.New("subscription conflict")
	ErrNotFound    = errors.New("subscription not found")
	ErrUnavailable = errors.New("subscription unavailable")
)

type Values struct {
	ContractKind    string  `json:"contract_kind,omitempty"`
	EffectiveDate   *string `json:"effective_date,omitempty"`
	AccessEndDate   *string `json:"access_end_date,omitempty"`
	Service         string  `json:"service"`
	Plan            *string `json:"plan"`
	Amount          *string `json:"amount"`
	Currency        *string `json:"currency"`
	IntervalUnit    *string `json:"interval_unit"`
	IntervalCount   *int    `json:"interval_count"`
	NextBillingDate *string `json:"next_billing_date"`
	Status          string  `json:"status"`
}
type Source struct {
	AccountID     *string  `json:"account_id,omitempty"`
	AccountLabel  *string  `json:"account_label,omitempty"`
	Provider      *string  `json:"provider,omitempty"`
	Provenance    string   `json:"provenance"`
	ResultID      string   `json:"result_id"`
	EventIndex    int      `json:"event_index"`
	OriginalEvent ai.Event `json:"original_event"`
}
type Subscription struct {
	Provenance string `json:"provenance"`
	ID         string `json:"id"`
	Version    int64  `json:"version"`
	Values
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Sources   []Source  `json:"sources"`
}
type CreateInput struct {
	Values
	RequestID        string  `json:"request_id"`
	SourceResultID   *string `json:"source_result_id"`
	SourceEventIndex *int    `json:"source_event_index"`
	Confirmed        bool    `json:"confirmed"`
}
type UpdateInput struct {
	Values
	// Legacy HTTP editors omit the entire lifecycle group. Preserve it under the
	// same version lock rather than clearing facts they have never displayed.
	PreserveLifecycle bool  `json:"-"`
	ExpectedVersion   int64 `json:"expected_version"`
	Confirmed         bool  `json:"confirmed"`
}
type DeleteInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	Confirmed       bool  `json:"confirmed"`
}
type SourceInput struct {
	ExpectedVersion  int64  `json:"expected_version"`
	SourceResultID   string `json:"source_result_id"`
	SourceEventIndex int    `json:"source_event_index"`
	Confirmed        bool   `json:"confirmed"`
}
type Filter struct {
	AccountID string
	Query     string
	Status    string
	Sort      string
	Limit     int
	Offset    int
}
type Page struct {
	Subscriptions []Subscription `json:"subscriptions"`
	Total         int            `json:"total"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
}
type AnalysisFilter struct {
	AccountID string
	State     string
	Review    string
	Limit     int
	Offset    int
}
type Registration struct {
	EventIndex     int    `json:"event_index"`
	SubscriptionID string `json:"subscription_id"`
}
type AnalysisResult struct {
	mailprocessing.Result
	AccountID         string                     `json:"account_id"`
	AccountLabel      string                     `json:"account_label"`
	Registrations     []Registration             `json:"registrations"`
	AutomationState   string                     `json:"automation_state"`
	Automation        []AutomationOutcome        `json:"automation"`
	BillingAutomation []BillingAutomationOutcome `json:"billing_automation"`
}
type BillingAutomationOutcome struct {
	EventIndex int     `json:"event_index"`
	State      string  `json:"state"`
	Reason     string  `json:"reason"`
	EntryID    *string `json:"entry_id"`
}
type AutomationOutcome struct {
	EventIndex     int     `json:"event_index"`
	State          string  `json:"state"`
	Reason         string  `json:"reason"`
	SubscriptionID *string `json:"subscription_id"`
}
type AnalysisPage struct {
	Results []AnalysisResult `json:"results"`
	Total   int              `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}
type AnalysisRun struct {
	mailprocessing.Run
	AccountLabel string `json:"account_label"`
}
type RunPage struct {
	Runs   []AnalysisRun `json:"runs"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}
