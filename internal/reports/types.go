// Package reports computes owner-scoped subscription analytics without external calls.
package reports

import (
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid report request")
	ErrNotFound    = errors.New("report not found")
	ErrUnavailable = errors.New("report unavailable")
)

type Contract struct {
	DisplayName           *string `json:"display_name"`
	NextBillingMode       string  `json:"next_billing_mode"`
	ID                    string  `json:"id"`
	Service               string  `json:"service"`
	Plan                  *string `json:"plan"`
	Status                string  `json:"status"`
	ContractKind          string  `json:"contract_kind"`
	AccessEndDate         *string `json:"access_end_date"`
	TrialEndDate          *string `json:"trial_end_date"`
	AutoRenew             *bool   `json:"auto_renew"`
	BillingDateSuppressed bool    `json:"billing_date_suppressed"`
	Amount                *string `json:"amount"`
	Currency              *string `json:"currency"`
	IntervalUnit          *string `json:"interval_unit"`
	IntervalCount         *int    `json:"interval_count"`
	NextBillingDate       *string `json:"next_billing_date"`
	MonthlyAmount         *string `json:"monthly_amount"`
	AnnualAmount          *string `json:"annual_amount"`
	ExclusionReason       string  `json:"exclusion_reason"`
}

type Record struct {
	DisplayName    *string
	SubscriptionID string
	Service        string
	Plan           *string
	Month          string
	Currency       string
	Kind           string
	Amount         string
	Count          int
}

type CurrencySummary struct {
	Currency         string  `json:"currency"`
	MonthlyAmount    *string `json:"monthly_amount"`
	AnnualAmount     *string `json:"annual_amount"`
	ContractCount    int     `json:"contract_count"`
	Next30DaysAmount *string `json:"next_30_days_amount"`
	Next30DaysCount  int     `json:"next_30_days_count"`
}

type ActualCurrency struct {
	Currency      string  `json:"currency"`
	PaymentAmount *string `json:"payment_amount"`
	RefundAmount  *string `json:"refund_amount"`
	NetAmount     *string `json:"net_amount"`
	InvoiceAmount *string `json:"invoice_amount"`
	PaymentCount  int     `json:"payment_count"`
	RefundCount   int     `json:"refund_count"`
	InvoiceCount  int     `json:"invoice_count"`
}

type HistoryBucket struct {
	Month      string           `json:"month"`
	Currencies []ActualCurrency `json:"currencies"`
}

type MonthlyService struct {
	DisplayName    *string `json:"display_name"`
	SubscriptionID string  `json:"subscription_id"`
	Service        string  `json:"service"`
	Plan           *string `json:"plan"`
	ActualCurrency
}

type Due struct {
	DisplayName     *string `json:"display_name"`
	NextBillingMode string  `json:"next_billing_mode"`
	SubscriptionID  string  `json:"subscription_id"`
	Service         string  `json:"service"`
	Plan            *string `json:"plan"`
	Date            string  `json:"date"`
	Amount          *string `json:"amount"`
	Currency        *string `json:"currency"`
	AnnualRenewal   bool    `json:"annual_renewal"`
	Status          string  `json:"status"`
}

type Coverage struct {
	TotalContracts       int `json:"total_contracts"`
	OpenContracts        int `json:"open_contracts"`
	ActiveContracts      int `json:"active_contracts"`
	UnknownContracts     int `json:"unknown_contracts"`
	EndedContracts       int `json:"ended_contracts"`
	FixedTermContracts   int `json:"fixed_term_contracts"`
	NormalizedContracts  int `json:"normalized_contracts"`
	UnpricedContracts    int `json:"unpriced_contracts"`
	MissingDateContracts int `json:"missing_date_contracts"`
	OverdueContracts     int `json:"overdue_contracts"`
	ActualRecordCount    int `json:"actual_record_count"`
}

type Report struct {
	AsOf            string            `json:"as_of"`
	GeneratedAt     time.Time         `json:"generated_at"`
	Timezone        string            `json:"timezone"`
	Month           string            `json:"month"`
	HistoryStart    string            `json:"history_start"`
	HistoryEnd      string            `json:"history_end"`
	Contracts       []Contract        `json:"contracts"`
	Currencies      []CurrencySummary `json:"currencies"`
	History         []HistoryBucket   `json:"history"`
	MonthlyServices []MonthlyService  `json:"monthly_services"`
	Upcoming        []Due             `json:"upcoming"`
	Coverage        Coverage          `json:"coverage"`
}
