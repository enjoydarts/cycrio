// Package mailprocessing executes explicitly confirmed, owner-scoped mail analysis.
package mailprocessing

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"time"
)

var (
	ErrInvalid     = mailaccounts.ErrInvalid
	ErrConflict    = mailaccounts.ErrConflict
	ErrNotFound    = mailaccounts.ErrNotFound
	ErrUnavailable = mailaccounts.ErrUnavailable
)

type PolicyInput struct {
	ExpectedVersion    int64  `json:"expected_version"`
	ProfileID          string `json:"profile_id"`
	Enabled            bool   `json:"enabled"`
	PrimaryRetention   string `json:"primary_retention"`
	SecondaryRetention string `json:"secondary_retention"`
	PrimaryConfirmed   bool   `json:"primary_confirmed"`
	SecondaryConfirmed bool   `json:"secondary_confirmed"`
}
type Policy struct {
	ProfileID          string `json:"profile_id"`
	Version            int64  `json:"version"`
	Enabled            bool   `json:"enabled"`
	PrimaryRetention   string `json:"primary_retention"`
	SecondaryRetention string `json:"secondary_retention"`
	PrimaryConfirmed   bool   `json:"primary_confirmed"`
	SecondaryConfirmed bool   `json:"secondary_confirmed"`
}

// PublicModel omits key identities and private workspace configuration.
type PublicModel struct {
	Provider        string              `json:"provider"`
	ModelID         string              `json:"model_id"`
	DisplayName     string              `json:"display_name"`
	Price           *modelcatalog.Price `json:"price"`
	Retention       string              `json:"retention"`
	RoutingProvider string              `json:"routing_provider"`
}
type PublicProfile struct {
	ID              string      `json:"id"`
	Primary         PublicModel `json:"primary"`
	Secondary       PublicModel `json:"secondary"`
	MaxInputBytes   int         `json:"max_input_bytes"`
	MaxOutputTokens int         `json:"max_output_tokens"`
	TimeoutSeconds  int         `json:"timeout_seconds"`
}
type PolicyState struct {
	RuntimeVersion int64          `json:"runtime_version"`
	Profile        *PublicProfile `json:"profile"`
	Policy         *Policy        `json:"policy"`
}
type Preview struct {
	AccountID       string       `json:"account_id"`
	ExpectedVersion int64        `json:"expected_version"`
	RuntimeVersion  int64        `json:"runtime_version"`
	ProfileID       *string      `json:"profile_id"`
	PolicyVersion   int64        `json:"policy_version"`
	Ready           bool         `json:"ready"`
	BlockedReason   string       `json:"blocked_reason"`
	PendingCount    int          `json:"pending_count"`
	FailedCount     int          `json:"failed_count"`
	UnknownCount    int          `json:"unknown_count"`
	Primary         *PublicModel `json:"primary"`
	Secondary       *PublicModel `json:"secondary"`
	MaxInputBytes   int          `json:"max_input_bytes"`
	MaxOutputTokens int          `json:"max_output_tokens"`
	TimeoutSeconds  int          `json:"timeout_seconds"`
}
type Input struct {
	ExpectedVersion int64  `json:"expected_version"`
	RuntimeVersion  int64  `json:"runtime_version"`
	ProfileID       string `json:"profile_id"`
	PolicyVersion   int64  `json:"policy_version"`
	RequestID       string `json:"request_id"`
	MaxMessages     int    `json:"max_messages"`
	Confirmed       bool   `json:"confirmed"`
}
type RetryInput struct {
	Input
	ExpectedResultID string `json:"expected_result_id"`
	ConfirmUnknown   bool   `json:"confirm_unknown"`
}
type Run struct {
	ID             string     `json:"id"`
	AccountID      string     `json:"account_id"`
	State          string     `json:"state"`
	MaxMessages    int        `json:"max_messages"`
	TotalCount     int        `json:"total_count"`
	CompletedCount int        `json:"completed_count"`
	RelatedCount   int        `json:"related_count"`
	UnrelatedCount int        `json:"unrelated_count"`
	FailedCount    int        `json:"failed_count"`
	UnknownCount   int        `json:"unknown_count"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}
type Result struct {
	ID             string     `json:"id"`
	MessageID      string     `json:"message_id"`
	State          string     `json:"state"`
	Classification *string    `json:"classification"`
	ErrorCode      string     `json:"error_code"`
	Events         []ai.Event `json:"events"`
	FinishedAt     time.Time  `json:"finished_at"`
}
type Lease struct {
	RunID   string `json:"run_id"`
	LeaseID string `json:"lease_id"`
}
type Work struct {
	Run        Run                            `json:"-"`
	AttemptID  string                         `json:"-"`
	OwnerID    string                         `json:"-"`
	Profile    airuntime.Profile              `json:"-"`
	Credential mailaccounts.CredentialVersion `json:"-"`
	FolderID   string                         `json:"-"`
	MessageID  string                         `json:"-"`
	Envelope   secretstore.Envelope           `json:"-"`
	FolderName string                         `json:"-"`
}
type Completion struct {
	State          string
	Classification *string
	ErrorCode      string
	Events         []ai.Event
}
type InferenceClient interface {
	Do(context.Context, ai.Request) (ai.Response, error)
}
type CredentialResolver func(context.Context, string) ([]byte, error)
type Repository interface {
	AdminRead(context.Context, string) (PolicyState, error)
	SavePolicy(context.Context, string, PolicyInput) (PolicyState, error)
	Preview(context.Context, string, string) (Preview, error)
	Request(context.Context, string, string, Input) (Run, error)
	Retry(context.Context, string, string, string, RetryInput) (Run, error)
	ListRuns(context.Context, string, string) ([]Run, error)
	Results(context.Context, string, string) ([]Result, error)
	Cancel(context.Context, string, string, string) (Run, error)
	Claim(context.Context) ([]Lease, error)
	Start(context.Context, Lease) (Work, error)
	BeginCall(context.Context, Lease, string) (airuntime.Call, error)
	FinishCall(context.Context, string, airuntime.CallUpdate) (airuntime.Call, error)
	Finish(context.Context, Lease, Completion) error
}

func Public(p airuntime.Profile, policy Policy) PublicProfile {
	model := func(m airuntime.ModelRef, retention string) PublicModel {
		return PublicModel{Provider: m.Provider, ModelID: m.ModelID, DisplayName: m.DisplayName, Price: m.Price, Retention: retention}
	}
	secondary := model(p.Secondary, policy.SecondaryRetention)
	if p.Secondary.Provider == "openrouter" {
		secondary.RoutingProvider = p.OpenRouterProvider
	}
	return PublicProfile{p.ID, model(p.Primary, policy.PrimaryRetention), secondary, p.MaxInputBytes, p.MaxOutputTokens, p.TimeoutSeconds}
}
