// Package priceresearch manages independent, administrator selected pricing research.
package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"time"
)

var (
	ErrInvalid             = errors.New("invalid price research input")
	ErrConflict            = errors.New("price research conflict")
	ErrNotFound            = errors.New("price research not found")
	ErrUnavailable         = errors.New("price research unavailable")
	ErrCredentialsRequired = errors.New("price research credentials required")
	ErrDispatched          = errors.New("price research already dispatched")
)

type Request struct {
	TestOnly        bool            `json:"test_only"`
	Capabilities    json.RawMessage `json:"capabilities"`
	Provider        string
	ModelID         string
	WorkspaceID     string
	Key             []byte `json:"-"`
	Targets         []Target
	MaxOutputTokens int
}
type Target struct {
	ID          string
	Provider    string
	ModelID     string
	DisplayName string
}
type Result struct {
	ToolCallLimit    int
	ToolActions      []SearchAction
	ResponseState    string
	FailureCode      string
	HTTPStatus       int
	ModelKind        string
	Models           []Quote
	CallsRecorded    bool
	Quotes           []Quote
	Sources          []string
	Usage            ai.Usage
	Cost             ai.Cost
	GenerationID     string
	SearchCalls      int
	SearchCallsKnown bool
}
type SearchAction struct {
	Action string `json:"action"`
	Status string `json:"status"`
}
type Quote struct {
	ModelKind               string  `json:"model_kind,omitempty"`
	ModelID                 string  `json:"model_id"`
	Provider                string  `json:"provider"`
	Currency                string  `json:"currency"`
	InputPerMillion         string  `json:"input_per_million"`
	OutputPerMillion        string  `json:"output_per_million"`
	CacheReadPerMillion     *string `json:"cache_read_per_million"`
	CacheWritePerMillion    *string `json:"cache_write_per_million"`
	CacheWrite30mPerMillion *string `json:"cache_write_30m_per_million"`
	CacheWrite1hPerMillion  *string `json:"cache_write_1h_per_million"`
	CacheWrite12hPerMillion *string `json:"cache_write_12h_per_million"`
	MaxInputTokens          *int64  `json:"max_input_tokens"`
	SourceURL               string  `json:"source_url"`
	Evidence                string  `json:"evidence"`
	Conditions              string  `json:"conditions"`
	TextInput               *bool   `json:"text_input"`
	StructuredOutput        *bool   `json:"structured_output"`
}
type Client interface {
	Research(context.Context, Request) (Result, error)
}
type State struct {
	Version          int64      `json:"version"`
	Provider         string     `json:"provider"`
	ModelID          *string    `json:"model_id"`
	AutomaticEnabled bool       `json:"automatic_enabled"`
	IntervalHours    int        `json:"interval_hours"`
	NextRunAt        *time.Time `json:"next_run_at"`
	Ready            bool       `json:"ready"`
	LatestRun        *Run       `json:"latest_run"`
}
type ConfigInput struct {
	ExpectedVersion  int64  `json:"expected_version"`
	ModelID          string `json:"model_id"`
	AutomaticEnabled bool   `json:"automatic_enabled"`
	IntervalHours    int    `json:"interval_hours"`
}
type RunInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	TargetProvider  string `json:"target_provider"`
}
type Run struct {
	TargetCount       int        `json:"target_count"`
	CompletedCount    int        `json:"completed_count"`
	NonTextCount      int        `json:"non_text_count"`
	UnclassifiedCount int        `json:"unclassified_count"`
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	TargetProvider    string     `json:"target_provider"`
	State             string     `json:"state"`
	ErrorCode         string     `json:"error_code"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	UpdatedCount      int        `json:"updated_count"`
	UnresolvedCount   int        `json:"unresolved_count"`
	CallID            *string    `json:"call_id"`
	Sources           []string   `json:"sources"`
}
type Lease struct {
	RunID   string `json:"run_id"`
	LeaseID string `json:"lease_id"`
}
type Execution struct {
	Run          Run
	Version      int64
	Model        modelcatalog.Model
	CredentialID string
	Targets      []Target
	WorkspaceID  string
}
type Repository interface {
	State(context.Context) (State, error)
	Configure(context.Context, string, ConfigInput) (State, error)
	Request(context.Context, string, string, RunInput) (Run, error)
	Run(context.Context, string) (Run, error)
	ClaimDue(context.Context) ([]Lease, error)
	Prepare(context.Context, Lease) (Execution, error)
	Begin(context.Context, Lease) error
	Complete(context.Context, Lease, Execution, Result, string) error
	QueueAutomatic(context.Context, string) error
}
type CredentialResolver func(context.Context, string) ([]byte, error)
