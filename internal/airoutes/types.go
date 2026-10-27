// Package airoutes manages non-inference OpenRouter ZDR endpoint metadata.
package airoutes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

const SourceURL = "https://openrouter.ai/api/v1/endpoints/zdr"
const DocumentationURL = "https://openrouter.ai/docs/api/api-reference/endpoints/list-endpoints-zdr"
const MaxBodyBytes = 8 << 20
const MaxRows = 20000
const MaxRowBytes = 16 << 10
const FreshFor = 15 * time.Minute

var (
	ErrInvalid             = errors.New("invalid route request")
	ErrConflict            = errors.New("route conflict")
	ErrUnavailable         = errors.New("route metadata unavailable")
	ErrCredentialsRequired = errors.New("route credentials required")
)

type Snapshot struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"captured_at"`
	SourceURL  string    `json:"source_url"`
}

// Proof contains only immutable upstream facts. Eligibility is evaluated separately.
type Proof struct {
	Ambiguous           bool                `json:"ambiguous"`
	ID                  string              `json:"id"`
	SnapshotID          string              `json:"snapshot_id"`
	CapturedAt          time.Time           `json:"captured_at"`
	SourceURL           string              `json:"source_url"`
	UpstreamModelID     string              `json:"upstream_model_id"`
	ModelName           string              `json:"model_name"`
	ProviderName        string              `json:"provider_name"`
	Tag                 string              `json:"tag"`
	ServiceTier         string              `json:"service_tier"`
	Quantization        string              `json:"quantization"`
	ContextLength       *int64              `json:"context_length"`
	MaxPromptTokens     *int64              `json:"max_prompt_tokens"`
	MaxCompletionTokens *int64              `json:"max_completion_tokens"`
	SupportedParameters []string            `json:"supported_parameters"`
	Status              *int                `json:"status"`
	ZDR                 bool                `json:"zdr"`
	Price               *modelcatalog.Price `json:"price"`
	PricingHash         string              `json:"pricing_hash"`
	RawMetadata         json.RawMessage     `json:"-"`
}
type Route struct {
	Proof
	ModelID  *string  `json:"model_id"`
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons"`
}
type Limits struct {
	MaxInputBytes   int
	MaxOutputTokens int
}
type Filter struct {
	Provider, ModelID, Search                     string
	MaxInputBytes, MaxOutputTokens, Limit, Offset int
	IncludeUnavailable                            bool
}
type RefreshState struct {
	Status        string     `json:"status"`
	ErrorCode     *string    `json:"error_code"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
}
type ListResult struct {
	Snapshot     *Snapshot    `json:"snapshot"`
	Routes       []Route      `json:"routes"`
	Total        int          `json:"total"`
	KeyAvailable bool         `json:"key_available"`
	Fresh        bool         `json:"fresh"`
	Refresh      RefreshState `json:"refresh"`
}
type Lease struct {
	ID, CredentialID  string
	CredentialVersion int64
	Owner             bool
}
type Repository interface {
	List(context.Context, Filter) (ListResult, error)
	Claim(context.Context, string) (Lease, error)
	Validate(context.Context, string, Lease) error
	Apply(context.Context, string, Lease, []Route) error
	Finish(context.Context, Lease, string) error
}
type CredentialResolver func(context.Context, string) ([]byte, error)
