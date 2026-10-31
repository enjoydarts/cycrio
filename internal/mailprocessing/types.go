// Package mailprocessing executes explicitly confirmed, owner-scoped mail analysis.
package mailprocessing

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airoutes"
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
	FullEmailAllowed   *bool  `json:"full_email_allowed,omitempty" nonnull:"true"`
	ExpectedVersion    int64  `json:"expected_version"`
	ProfileID          string `json:"profile_id"`
	Enabled            bool   `json:"enabled"`
	PrimaryRetention   string `json:"primary_retention"`
	SecondaryRetention string `json:"secondary_retention"`
	PrimaryConfirmed   bool   `json:"primary_confirmed"`
	SecondaryConfirmed bool   `json:"secondary_confirmed"`
}
type Policy struct {
	FullEmailAllowed   bool           `json:"full_email_allowed"`
	StandardProof      *StandardProof `json:"standard_proof,omitempty"`
	ProfileID          string         `json:"profile_id"`
	Version            int64          `json:"version"`
	Enabled            bool           `json:"enabled"`
	PrimaryRetention   string         `json:"primary_retention"`
	SecondaryRetention string         `json:"secondary_retention"`
	PrimaryConfirmed   bool           `json:"primary_confirmed"`
	SecondaryConfirmed bool           `json:"secondary_confirmed"`
}

// PublicModel omits key identities and private workspace configuration.
type PublicModel struct {
	RetentionSourceURL string              `json:"retention_source_url,omitempty"`
	Provider           string              `json:"provider"`
	ModelID            string              `json:"model_id"`
	DisplayName        string              `json:"display_name"`
	Price              *modelcatalog.Price `json:"price"`
	Retention          string              `json:"retention"`
	RoutingProvider    string              `json:"routing_provider"`
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
	StandardTerms  *StandardTermsState `json:"standard_terms,omitempty"`
	RuntimeVersion int64               `json:"runtime_version"`
	Profile        *PublicProfile      `json:"profile"`
	Policy         *Policy             `json:"policy"`
}
type Preview struct {
	FullEmailAllowed bool         `json:"full_email_allowed"`
	AccountID        string       `json:"account_id"`
	ExpectedVersion  int64        `json:"expected_version"`
	RuntimeVersion   int64        `json:"runtime_version"`
	ProfileID        *string      `json:"profile_id"`
	PolicyVersion    int64        `json:"policy_version"`
	Ready            bool         `json:"ready"`
	BlockedReason    string       `json:"blocked_reason"`
	PendingCount     int          `json:"pending_count"`
	FailedCount      int          `json:"failed_count"`
	UnknownCount     int          `json:"unknown_count"`
	Primary          *PublicModel `json:"primary"`
	Secondary        *PublicModel `json:"secondary"`
	MaxInputBytes    int          `json:"max_input_bytes"`
	MaxOutputTokens  int          `json:"max_output_tokens"`
	TimeoutSeconds   int          `json:"timeout_seconds"`
}
type Input struct {
	HistoryID       string `json:"history_id,omitempty" nonnull:"true"`
	FullEmail       bool   `json:"full_email,omitempty" nonnull:"true"`
	ExpectedVersion int64  `json:"expected_version"`
	RuntimeVersion  int64  `json:"runtime_version"`
	ProfileID       string `json:"profile_id"`
	PolicyVersion   int64  `json:"policy_version"`
	RequestID       string `json:"request_id"`
	MaxMessages     int    `json:"max_messages"`
	Confirmed       bool   `json:"confirmed"`
}

// ImportHistory groups automatic batches by confirmed scope, and manual work
// by its original request. Dates are unknown for legacy manual requests.
type ImportHistory struct {
	ActiveRunID     *string   `json:"active_run_id"`
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	CreatedAt       time.Time `json:"created_at"`
	Since           *string   `json:"since"`
	Before          *string   `json:"before"`
	State           string    `json:"state"`
	IsCurrent       bool      `json:"is_current"`
	Incremental     bool      `json:"incremental"`
	DiscoveredCount int       `json:"discovered_count"`
	PendingCount    int       `json:"pending_count"`
	CompletedCount  int       `json:"completed_count"`
	CandidateCount  int       `json:"candidate_count"`
	RegisteredCount int       `json:"registered_count"`
	FailedCount     int       `json:"failed_count"`
	UnknownCount    int       `json:"unknown_count"`
}
type ImportHistoryPage struct {
	History []ImportHistory `json:"history"`
	Total   int             `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}
type RetryInput struct {
	Input
	ExpectedResultID string `json:"expected_result_id"`
	ConfirmUnknown   bool   `json:"confirm_unknown"`
}
type RetryBatch struct {
	Run           Run `json:"run"`
	AcceptedCount int `json:"accepted_count"`
	SkippedCount  int `json:"skipped_count"`
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
	ReaderFolders []string                       `json:"-"`
	Automatic     bool                           `json:"-"`
	FullEmail     bool                           `json:"-"`
	Run           Run                            `json:"-"`
	AttemptID     string                         `json:"-"`
	OwnerID       string                         `json:"-"`
	Profile       airuntime.Profile              `json:"-"`
	Credential    mailaccounts.CredentialVersion `json:"-"`
	FolderID      string                         `json:"-"`
	MessageID     string                         `json:"-"`
	Envelope      secretstore.Envelope           `json:"-"`
	FolderName    string                         `json:"-"`
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
	primary := model(p.Primary, policy.PrimaryRetention)
	secondary := model(p.Secondary, policy.SecondaryRetention)
	if p.Secondary.Provider == "openrouter" {
		secondary.RoutingProvider = p.OpenRouterProvider
	}
	// Only publish official sources from the policy's own frozen proof. Legacy
	// custom text and unrelated proof metadata never establish a known source.
	if proof := policy.StandardProof; proof != nil && policy.ProfileID == p.ID && proof.ProfileID == p.ID && proof.PolicyVersion == policy.Version {
		if p.Primary.Provider == "liquid" && proof.PrimaryMode == "standard" && proof.PrimaryTerms != nil && proof.PrimaryTerms.SourceURL == CurrentPrimaryTerms().SourceURL {
			primary.RetentionSourceURL = proof.PrimaryTerms.SourceURL
		}
		if route := proof.SecondaryRoute; p.Secondary.Provider == "openrouter" && route.UpstreamModelID == p.Secondary.ModelID && route.Tag == p.OpenRouterProvider && route.ZDR && route.SourceURL == airoutes.SourceURL {
			secondary.RetentionSourceURL = route.SourceURL
		}
	}
	return PublicProfile{p.ID, primary, secondary, p.MaxInputBytes, p.MaxOutputTokens, p.TimeoutSeconds}
}
