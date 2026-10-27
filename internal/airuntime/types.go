// Package airuntime manages immutable AI execution settings and usage accounting.
package airuntime

import (
	"context"
	"errors"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

var (
	ErrInvalid             = errors.New("invalid AI runtime request")
	ErrConflict            = errors.New("AI runtime version conflict")
	ErrNotFound            = errors.New("AI runtime item not found")
	ErrUnavailable         = errors.New("AI runtime unavailable")
	ErrCredentialsRequired = errors.New("AI credentials required")
)

type ModelRef struct {
	ID                string              `json:"id"`
	Provider          string              `json:"provider"`
	ModelID           string              `json:"model_id"`
	DisplayName       string              `json:"display_name"`
	CredentialID      string              `json:"credential_id"`
	CredentialVersion int64               `json:"credential_version"`
	Price             *modelcatalog.Price `json:"price"`
}
type Profile struct {
	RouteProof           *airoutes.Proof `json:"route_proof,omitempty"`
	ID                   string          `json:"id"`
	CatalogSettingsID    string          `json:"catalog_settings_id"`
	Primary              ModelRef        `json:"primary"`
	Secondary            ModelRef        `json:"secondary"`
	MaxInputBytes        int             `json:"max_input_bytes"`
	MaxOutputTokens      int             `json:"max_output_tokens"`
	TimeoutSeconds       int             `json:"timeout_seconds"`
	OpenRouterProvider   string          `json:"openrouter_provider"`
	AnthropicWorkspaceID string          `json:"anthropic_workspace_id"`
	PromptVersion        string          `json:"prompt_version"`
	CreatedAt            time.Time       `json:"created_at"`
}
type ConfigInput struct {
	RouteID            string `json:"route_id,omitempty"`
	ExpectedVersion    int64  `json:"expected_version"`
	CatalogSettingsID  string `json:"catalog_settings_id"`
	MaxInputBytes      int    `json:"max_input_bytes"`
	MaxOutputTokens    int    `json:"max_output_tokens"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	OpenRouterProvider string `json:"openrouter_provider"`
}
type State struct {
	Version             int64    `json:"version"`
	Draft               *Profile `json:"draft"`
	Active              *Profile `json:"active"`
	ActiveReady         bool     `json:"active_ready"`
	ActiveBlockedReason *string  `json:"active_blocked_reason"`
	LatestTest          *TestRun `json:"latest_test"`
}
type TestInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	ProfileID       string `json:"profile_id"`
	RequestID       string `json:"request_id"`
}
type ActivationInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	ProfileID       string `json:"profile_id"`
	TestID          string `json:"test_id"`
}
type TestRun struct {
	PrimaryFailure   *TestFailure `json:"primary_failure,omitempty"`
	SecondaryFailure *TestFailure `json:"secondary_failure,omitempty"`
	ID               string       `json:"id"`
	ProfileID        string       `json:"profile_id"`
	State            string       `json:"state"`
	PrimaryOutcome   *string      `json:"primary_outcome"`
	SecondaryOutcome *string      `json:"secondary_outcome"`
	Classification   *string      `json:"classification"`
	ExtractedEvents  *int         `json:"extracted_events"`
	CreatedAt        time.Time    `json:"created_at"`
	StartedAt        *time.Time   `json:"started_at"`
	FinishedAt       *time.Time   `json:"finished_at"`
}
type Lease struct {
	TestRunID string `json:"test_run_id"`
	LeaseID   string `json:"lease_id"`
}
type Execution struct {
	Run     TestRun
	Profile Profile
	ActorID string
}
type TestCompletion struct {
	State                            string
	PrimaryOutcome, SecondaryOutcome *string
	Classification                   *string
	ExtractedEvents                  *int
}
type BeginCallInput struct {
	RunID             *string
	OperationID       string
	ProfileID         string
	Stage             string
	AttemptNo         int
	Scope             string
	ConsumingUserID   *string
	InitiatedByUserID *string
}
type CallUpdate struct {
	Outcome        string
	HTTPStatus     int
	GenerationID   string
	ActualModelID  string
	ActualProvider string
	Usage          ai.Usage
	Cost           ai.Cost
}
type Call struct {
	ID                string     `json:"id"`
	OperationID       string     `json:"operation_id"`
	RunID             *string    `json:"run_id"`
	ProfileID         string     `json:"profile_id"`
	Scope             string     `json:"scope"`
	ConsumingUserID   *string    `json:"consuming_user_id"`
	InitiatedByUserID *string    `json:"initiated_by_user_id"`
	Provider          string     `json:"provider"`
	ModelID           string     `json:"model_id"`
	ModelName         string     `json:"model_name"`
	RequestedModelID  string     `json:"requested_model_id"`
	CredentialID      string     `json:"-"`
	PriceID           *string    `json:"price_id"`
	Stage             string     `json:"stage"`
	AttemptNo         int        `json:"attempt_no"`
	Status            string     `json:"status"`
	Outcome           *string    `json:"outcome"`
	HTTPStatus        *int       `json:"http_status"`
	GenerationID      *string    `json:"generation_id"`
	ActualModelID     *string    `json:"actual_model_id"`
	ActualProvider    *string    `json:"actual_provider"`
	Usage             ai.Usage   `json:"usage"`
	Cost              ai.Cost    `json:"cost"`
	RevisionID        *string    `json:"revision_id"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
type CostFilter struct {
	Month, UserID, Scope, Provider, ModelID, Stage string
	Limit, Offset                                  int
}
type Totals struct {
	Currency        *string `json:"currency"`
	Amount          string  `json:"amount"`
	EstimatedAmount string  `json:"estimated_amount"`
	ReportedAmount  string  `json:"reported_amount"`
	Calls           int64   `json:"calls"`
	UnknownCalls    int64   `json:"unknown_calls"`
	Failures        int64   `json:"failures"`
}
type ConsumerCost struct {
	UserID       string   `json:"user_id"`
	Calls        int64    `json:"calls"`
	UnknownCalls int64    `json:"unknown_calls"`
	Failures     int64    `json:"failures"`
	Totals       []Totals `json:"totals"`
}
type ScopeCost struct {
	Scope  string   `json:"scope"`
	Totals []Totals `json:"totals"`
}
type Breakdown struct {
	Provider  string   `json:"provider"`
	ModelID   string   `json:"model_id"`
	ModelName string   `json:"model_name"`
	Stage     string   `json:"stage"`
	Totals    []Totals `json:"totals"`
}
type CostReport struct {
	Month          string         `json:"month"`
	Timezone       string         `json:"timezone"`
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	Provisional    bool           `json:"provisional"`
	Totals         []Totals       `json:"totals"`
	Users          []ConsumerCost `json:"users"`
	UsersTotal     int            `json:"users_total"`
	Administration []ScopeCost    `json:"administration"`
	Breakdown      []Breakdown    `json:"breakdown"`
	UpdatedAt      *time.Time     `json:"updated_at"`
}
type CallPage struct {
	Calls []Call `json:"calls"`
	Total int    `json:"total"`
}
type UsageRevision struct {
	ID         string    `json:"id"`
	CallID     string    `json:"call_id"`
	Usage      ai.Usage  `json:"usage"`
	Cost       ai.Cost   `json:"cost"`
	CapturedAt time.Time `json:"captured_at"`
}

type Repository interface {
	State(context.Context) (State, error)
	SaveProfile(context.Context, string, ConfigInput) (State, error)
	RequestTest(context.Context, string, TestInput) (TestRun, error)
	Test(context.Context, string) (TestRun, error)
	Activate(context.Context, string, ActivationInput) (State, error)
	Deactivate(context.Context, string, int64) (State, error)
	Claim(context.Context) ([]Lease, error)
	StartTest(context.Context, Lease) (Execution, error)
	CheckExecution(context.Context, Execution, Lease) error
	FinishTest(context.Context, Lease, TestCompletion) error
	BeginCall(context.Context, BeginCallInput) (Call, error)
	FinishCall(context.Context, string, CallUpdate) (Call, error)
	GetCall(context.Context, string) (Call, error)
	Revisions(context.Context, string) ([]UsageRevision, error)
	ReconcileCall(context.Context, string, string, ai.Reconciliation) (Call, error)
	Costs(context.Context, CostFilter) (CostReport, error)
	Calls(context.Context, CostFilter) (CallPage, error)
}
