// Package notifications produces owner-scoped notification facts without
// accessing storage, email recipients, credentials, or original email content.
package notifications

import (
	"errors"
	"time"
)

var ErrInvalid = errors.New("invalid notification input")

const Timezone = "Asia/Tokyo"

type Kind string

const (
	Renewal       Kind = "renewal"
	AnnualCharge  Kind = "annual_charge"
	TrialEnd      Kind = "trial_end"
	PriceIncrease Kind = "price_increase"
	ImportError   Kind = "import_error"
	PendingReview Kind = "pending_review"
)

var kinds = []Kind{Renewal, AnnualCharge, TrialEnd, PriceIncrease, ImportError, PendingReview}

type Channels struct {
	InApp bool `json:"in_app"`
	Email bool `json:"email"`
	Push  bool `json:"push"`
}
type Preferences struct {
	Version    int64        `json:"version"`
	Kinds      KindChannels `json:"kinds"`
	LeadDays   int          `json:"lead_days"`
	SendHour   int          `json:"send_hour"`
	Timezone   string       `json:"timezone"`
	EmailOptIn bool         `json:"email_opt_in"`
	PushOptIn  bool         `json:"push_opt_in"`
}
type Contract struct {
	ID              string
	Service         string
	Status          string
	Deleted         bool
	ContractKind    string
	Amount          *string
	Currency        *string
	IntervalUnit    *string
	IntervalCount   *int
	NextBillingDate *string
	NextDateSource  string
	TrialEndDate    *string
	AutoRenew       *bool
	AccessEndDate   *string
}
type PendingPriceChange struct {
	ContractID  string
	ResultID    string
	EventIndex  int
	Pending     bool
	OldAmount   *string
	OldCurrency *string
	NewAmount   *string
	NewCurrency *string
}
type ImportEpisode struct {
	AccountID  string
	EpisodeID  string
	Active     bool
	ManualStop bool
	Reason     string
}
type PendingReviewRun struct {
	RunID    string
	Count    int
	Pending  bool
	Channels Channels
}
type GenerationInput struct {
	Now            time.Time
	Contracts      []Contract
	PriceChanges   []PendingPriceChange
	ImportEpisodes []ImportEpisode
	ReviewRuns     []PendingReviewRun
}
type Candidate struct {
	Kind        Kind      `json:"kind"`
	SourceKey   string    `json:"source_key"`
	ContractID  *string   `json:"contract_id,omitempty"`
	AccountID   *string   `json:"account_id,omitempty"`
	ResultID    *string   `json:"result_id,omitempty"`
	EventIndex  *int      `json:"event_index,omitempty"`
	EpisodeID   *string   `json:"episode_id,omitempty"`
	ReviewRunID *string   `json:"review_run_id,omitempty"`
	ReviewCount *int      `json:"review_count,omitempty"`
	Service     string    `json:"service"`
	DueDate     *string   `json:"due_date,omitempty"`
	ScheduledAt time.Time `json:"scheduled_at"`
	DateSource  string    `json:"date_source,omitempty"`
	Amount      *string   `json:"amount,omitempty"`
	Currency    *string   `json:"currency,omitempty"`
	OldAmount   *string   `json:"old_amount,omitempty"`
	NewAmount   *string   `json:"new_amount,omitempty"`
	InApp       bool      `json:"in_app"`
	Email       bool      `json:"email"`
	Push        bool      `json:"push"`
}
type EmailContent struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}
