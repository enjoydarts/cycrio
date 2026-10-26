// Package billingschedule calculates dates from explicitly confirmed calendar rules.
package billingschedule

import (
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

const AlgorithmVersion = "calendar-v1"
const Timezone = "Asia/Tokyo"

var (
	ErrInvalid     = errors.New("invalid billing schedule")
	ErrConflict    = errors.New("billing schedule conflict")
	ErrNotFound    = subscriptions.ErrNotFound
	ErrUnavailable = subscriptions.ErrUnavailable
)

type Conflict struct{ Reason string }

func (c *Conflict) Error() string { return "billing schedule conflict: " + c.Reason }
func (c *Conflict) Unwrap() error { return ErrConflict }

type Rule struct {
	IntervalUnit  string `json:"interval_unit"`
	IntervalCount int    `json:"interval_count"`
	AnchorDate    string `json:"anchor_date"`
	MonthEnd      bool   `json:"month_end"`
}
type Settings struct {
	HistoryEvidence    *HistoryEvidence `json:"history_evidence,omitempty"`
	Rule               Rule             `json:"rule"`
	Stale              bool             `json:"stale"`
	ConfirmedAsOf      string           `json:"confirmed_as_of"`
	ConfirmedCandidate string           `json:"confirmed_candidate"`
	AlgorithmVersion   string           `json:"algorithm_version"`
	ConfirmedVersion   int64            `json:"confirmed_version"`
	ConfirmedAt        time.Time        `json:"confirmed_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}
type View struct {
	Subscription   subscriptions.Subscription `json:"subscription"`
	Settings       *Settings                  `json:"settings"`
	AsOf           string                     `json:"as_of"`
	Timezone       string                     `json:"timezone"`
	NextDateSource string                     `json:"next_date_source"`
	Overdue        bool                       `json:"overdue"`
}
type Preview struct {
	Subscription     subscriptions.Subscription `json:"subscription"`
	Rule             Rule                       `json:"rule"`
	AsOf             string                     `json:"as_of"`
	Timezone         string                     `json:"timezone"`
	CandidateDate    *string                    `json:"candidate_date"`
	Reason           string                     `json:"reason"`
	AlgorithmVersion string                     `json:"algorithm_version"`
	Overdue          bool                       `json:"overdue"`
}
type SaveInput struct {
	Rule
	HistoryBasis      *HistoryBasis `json:"history_basis,omitempty"`
	RequestID         string        `json:"request_id"`
	ExpectedVersion   int64         `json:"expected_version"`
	ExpectedAsOf      string        `json:"expected_as_of"`
	ExpectedCandidate string        `json:"expected_candidate"`
	Confirmed         bool          `json:"confirmed"`
}
type Receipt struct {
	HistoryEvidence  *HistoryEvidence `json:"history_evidence,omitempty"`
	RequestID        string           `json:"request_id"`
	AppliedVersion   int64            `json:"applied_version"`
	Rule             Rule             `json:"rule"`
	AsOf             string           `json:"as_of"`
	CandidateDate    string           `json:"candidate_date"`
	AlgorithmVersion string           `json:"algorithm_version"`
	CreatedAt        time.Time        `json:"created_at"`
}
type SaveResult struct {
	Receipt Receipt `json:"receipt"`
	Current View    `json:"current"`
}
