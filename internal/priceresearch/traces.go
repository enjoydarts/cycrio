package priceresearch

import (
	"encoding/json"
	"time"
)

// TraceDocument contains only research request/response bodies, never headers.
// Persistence is encrypted and retrieval requires administrator authorization.
type TraceDocument struct {
	Request           json.RawMessage   `json:"request"`
	Response          string            `json:"response"`
	ResponseTruncated bool              `json:"response_truncated"`
	ReferencePrices   map[string]Quote  `json:"reference_prices"`
	Validation        map[string]string `json:"validation"`
}
type TraceSummary struct {
	CallID     string     `json:"call_id"`
	AttemptNo  int        `json:"attempt_no"`
	Status     string     `json:"status"`
	Available  bool       `json:"available"`
	CapturedAt *time.Time `json:"captured_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}
type TraceView struct {
	CallID     string        `json:"call_id"`
	CapturedAt time.Time     `json:"captured_at"`
	ExpiresAt  time.Time     `json:"expires_at"`
	Document   TraceDocument `json:"document"`
}
