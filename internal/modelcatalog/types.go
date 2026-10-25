// Package modelcatalog manages provider metadata and draft model settings; it never runs inference.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalid             = errors.New("invalid catalog input")
	ErrConflict            = errors.New("catalog version conflict")
	ErrNotFound            = errors.New("catalog item not found")
	ErrUnavailable         = errors.New("catalog unavailable")
	ErrCredentialsRequired = errors.New("catalog credentials required")
)

type Rate struct {
	Amount string `json:"amount"`
	Unit   string `json:"unit"`
}
type Price struct {
	ID            string          `json:"id"`
	ModelID       string          `json:"model_id"`
	Source        string          `json:"source"`
	Currency      string          `json:"currency"`
	Rates         map[string]Rate `json:"rates"`
	RawPricing    json.RawMessage `json:"raw_pricing"`
	EffectiveFrom *time.Time      `json:"effective_from"`
	CapturedAt    time.Time       `json:"captured_at"`
	SourceURL     string          `json:"source_url"`
}
type Model struct {
	ID                       string          `json:"id"`
	Provider                 string          `json:"provider"`
	ModelID                  string          `json:"model_id"`
	DisplayName              string          `json:"display_name"`
	CanonicalSlug            *string         `json:"canonical_slug"`
	TextInput                *bool           `json:"text_input"`
	StructuredOutput         *bool           `json:"structured_output"`
	ContextLength            *int64          `json:"context_length"`
	MaxInputTokens           *int64          `json:"max_input_tokens"`
	MaxOutputTokens          *int64          `json:"max_output_tokens"`
	RawMetadata              json.RawMessage `json:"raw_metadata"`
	Source                   string          `json:"source"`
	Available                bool            `json:"available"`
	Enabled                  bool            `json:"enabled"`
	CustomDisplayName        *string         `json:"custom_display_name"`
	TextInputOverride        *bool           `json:"text_input_override"`
	StructuredOutputOverride *bool           `json:"structured_output_override"`
	Version                  int64           `json:"version"`
	APIPrice                 *Price          `json:"api_price"`
	ManualPrice              *Price          `json:"manual_price"`
	CurrentPrice             *Price          `json:"current_price"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
}
type Filter struct {
	Provider        string
	Search          string
	IncludeDisabled bool
	Limit           int
	Offset          int
}
type ListResult struct {
	Models []Model `json:"models"`
	Total  int     `json:"total"`
}
type ManualModelInput struct {
	Provider         string `json:"provider"`
	ModelID          string `json:"model_id"`
	DisplayName      string `json:"display_name"`
	TextInput        *bool  `json:"text_input"`
	StructuredOutput *bool  `json:"structured_output"`
}
type ModelUpdate struct {
	ExpectedVersion  int64  `json:"expected_version"`
	DisplayName      string `json:"display_name"`
	Enabled          bool   `json:"enabled"`
	TextInput        *bool  `json:"text_input"`
	StructuredOutput *bool  `json:"structured_output"`
}
type ManualPriceInput struct {
	ExpectedVersion         int64      `json:"expected_version"`
	Currency                string     `json:"currency"`
	InputPerMillion         *string    `json:"input_per_million"`
	OutputPerMillion        *string    `json:"output_per_million"`
	CacheReadPerMillion     *string    `json:"cache_read_per_million"`
	CacheWritePerMillion    *string    `json:"cache_write_per_million"`
	CacheWrite30mPerMillion *string    `json:"cache_write_30m_per_million"`
	CacheWrite1hPerMillion  *string    `json:"cache_write_1h_per_million"`
	CacheWrite12hPerMillion *string    `json:"cache_write_12h_per_million"`
	PerRequest              *string    `json:"per_request"`
	EffectiveFrom           *time.Time `json:"effective_from"`
	SourceURL               string     `json:"source_url"`
}
type SyncState struct {
	WorkspaceID      string     `json:"workspace_id"`
	Provider         string     `json:"provider"`
	AutomaticEnabled bool       `json:"automatic_enabled"`
	IntervalHours    int        `json:"interval_hours"`
	Version          int64      `json:"version"`
	NextSyncAt       *time.Time `json:"next_sync_at"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	LastAttemptAt    *time.Time `json:"last_attempt_at"`
	Status           string     `json:"status"`
	ErrorCode        *string    `json:"error_code"`
}
type SyncConfigInput struct {
	ExpectedVersion  int64  `json:"expected_version"`
	AutomaticEnabled bool   `json:"automatic_enabled"`
	IntervalHours    int    `json:"interval_hours"`
	WorkspaceID      string `json:"workspace_id"`
}
type SyncLease struct {
	Provider string `json:"provider"`
	LeaseID  string `json:"lease_id"`
}
type SettingsState struct {
	Version          int64      `json:"version"`
	ID               *string    `json:"id"`
	PrimaryModelID   *string    `json:"primary_model_id"`
	SecondaryModelID *string    `json:"secondary_model_id"`
	PrimaryPriceID   *string    `json:"primary_price_id"`
	SecondaryPriceID *string    `json:"secondary_price_id"`
	UpdatedAt        *time.Time `json:"updated_at"`
}
type SettingsInput struct {
	ExpectedVersion  int64  `json:"expected_version"`
	PrimaryModelID   string `json:"primary_model_id"`
	SecondaryModelID string `json:"secondary_model_id"`
}
type CatalogEntry struct {
	Model Model
	Price *Price
}
type CredentialResolver func(context.Context, string) ([]byte, error)

type Repository interface {
	List(context.Context, Filter) (ListResult, error)
	GetModel(context.Context, string) (Model, error)
	AddModel(context.Context, string, ManualModelInput) (Model, error)
	UpdateModel(context.Context, string, string, ModelUpdate) (Model, error)
	Prices(context.Context, string) ([]Price, error)
	SavePrice(context.Context, string, string, int64, Price) (Model, error)
	ResetPrice(context.Context, string, string, int64) (Model, error)
	SyncStates(context.Context) ([]SyncState, error)
	RequestSync(context.Context, string, string) (SyncState, error)
	ConfigureSync(context.Context, string, string, SyncConfigInput) (SyncState, error)
	ClaimDue(context.Context) ([]SyncLease, error)
	ValidateLease(context.Context, string, string) error
	ApplySync(context.Context, string, string, []CatalogEntry) error
	FinishSync(context.Context, string, string, string) error
	Settings(context.Context) (SettingsState, error)
	SaveSettings(context.Context, string, SettingsInput) (SettingsState, error)
}

func ValidProvider(p string) bool {
	return p == "liquid" || p == "openrouter" || p == "openai" || p == "anthropic"
}
