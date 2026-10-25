// Package ai implements bounded, single-call provider adapters. Provider text and
// credentials are transient and must never be used as persistence metadata.
package ai

import (
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type RequestInput struct {
	Text         string `json:"-"`
	Instructions string `json:"-"`
}
type Request struct {
	Provider        string              `json:"provider"`
	ModelID         string              `json:"model_id"`
	ProviderSlug    string              `json:"provider_slug"`
	WorkspaceID     string              `json:"workspace_id"`
	Input           RequestInput        `json:"-"`
	Key             []byte              `json:"-"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	Price           *modelcatalog.Price `json:"-"`
}
type Usage struct {
	Present             bool  `json:"present"`
	Valid               bool  `json:"valid"`
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	OrdinaryInputTokens int64 `json:"ordinary_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheWriteTokens    int64 `json:"cache_write_tokens"`
	CacheWrite5mTokens  int64 `json:"cache_write_5m_tokens"`
	CacheWrite30mTokens int64 `json:"cache_write_30m_tokens"`
	CacheWrite1hTokens  int64 `json:"cache_write_1h_tokens"`
	CacheWrite12hTokens int64 `json:"cache_write_12h_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CacheWriteTTLKnown  bool  `json:"cache_write_ttl_known"`
}
type Cost struct {
	Status   string  `json:"status"`
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
	Reason   string  `json:"reason"`
}
type Classification struct {
	Label      string       `json:"label"`
	Confidence *json.Number `json:"confidence,omitempty"`
}
type Response struct {
	GenerationID   string          `json:"generation_id"`
	ActualModelID  string          `json:"actual_model_id"`
	ActualProvider string          `json:"actual_provider"`
	Usage          Usage           `json:"usage"`
	Cost           Cost            `json:"cost"`
	Classification *Classification `json:"classification,omitempty"`
	Extraction     *Extraction     `json:"-"`
}
type ReconcileRequest struct {
	GenerationID string
	Key          []byte `json:"-"`
}
type Reconciliation struct {
	GenerationID   string `json:"generation_id"`
	ActualModelID  string `json:"actual_model_id"`
	ActualProvider string `json:"actual_provider"`
	Usage          Usage  `json:"usage"`
	Cost           Cost   `json:"cost"`
}

// Error deliberately carries no provider text, URLs, or wrapped transport error.
type Error struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
}

func (e *Error) Error() string { return e.Code }

type HTTPError = Error

type Extraction struct {
	Events []Event `json:"events"`
}
type Event struct {
	Service       string  `json:"service"`
	Plan          *string `json:"plan"`
	EventType     string  `json:"event_type"`
	Amount        *string `json:"amount"`
	Currency      *string `json:"currency"`
	BilledAt      *string `json:"billed_at"`
	PeriodStart   *string `json:"period_start"`
	PeriodEnd     *string `json:"period_end"`
	NextBillingAt *string `json:"next_billing_at"`
	IntervalUnit  *string `json:"interval_unit"`
	IntervalCount *int64  `json:"interval_count"`
}
