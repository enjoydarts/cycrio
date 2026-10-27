// Package mailautomation owns explicit consent and durable automatic mailbox work.
package mailautomation

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"time"
)

var (
	ErrInvalid     = mailaccounts.ErrInvalid
	ErrConflict    = mailaccounts.ErrConflict
	ErrNotFound    = mailaccounts.ErrNotFound
	ErrUnavailable = mailaccounts.ErrUnavailable
)

type Scope struct {
	Since           string `json:"since"`
	Before          string `json:"before"`
	Timezone        string `json:"timezone"`
	FullEmail       bool   `json:"full_email"`
	IntervalSeconds int    `json:"interval_seconds"`
	AutoRegister    bool   `json:"auto_register"`
}
type Basis struct {
	AccountVersion int64  `json:"account_version"`
	RuntimeVersion int64  `json:"runtime_version"`
	ProfileID      string `json:"profile_id"`
	PolicyVersion  int64  `json:"policy_version"`
}
type Progress struct {
	BootstrapComplete  bool  `json:"bootstrap_complete"`
	HistoricalComplete bool  `json:"historical_complete"`
	DiscoveredCount    int64 `json:"discovered_count"`
	PendingCount       int64 `json:"pending_count"`
	CompletedCount     int64 `json:"completed_count"`
	FailedCount        int64 `json:"failed_count"`
	UnknownCount       int64 `json:"unknown_count"`
}
type State struct {
	ID                        string         `json:"id"`
	AccountID                 string         `json:"account_id"`
	Version                   int64          `json:"version"`
	Generation                int64          `json:"generation"`
	State                     string         `json:"state"`
	Reason                    string         `json:"reason"`
	Ready                     bool           `json:"ready"`
	PreparationReady          bool           `json:"preparation_ready"`
	PreparationBlockedReasons []string       `json:"preparation_blocked_reasons"`
	BlockedReasons            []string       `json:"blocked_reasons"`
	Scope                     *Scope         `json:"scope"`
	DefaultScope              Scope          `json:"default_scope"`
	Basis                     Basis          `json:"basis"`
	Progress                  Progress       `json:"progress"`
	LastSuccessAt             *time.Time     `json:"last_success_at"`
	NextCheckAt               *time.Time     `json:"next_check_at"`
	RecentSyncRuns            []mailsync.Run `json:"recent_sync_runs"`
	DiscoveryFailureCount     int            `json:"discovery_failure_count"`
	RetryAt                   *time.Time     `json:"retry_at"`
}
type Input struct {
	ExpectedSettingsVersion int64  `json:"expected_settings_version"`
	ExpectedAccountVersion  int64  `json:"expected_account_version"`
	RuntimeVersion          int64  `json:"runtime_version"`
	ProfileID               string `json:"profile_id"`
	PolicyVersion           int64  `json:"policy_version"`
	Since                   string `json:"since"`
	Before                  string `json:"before"`
	FullEmail               bool   `json:"full_email"`
	IntervalSeconds         int    `json:"interval_seconds"`
	Confirmed               bool   `json:"confirmed"`
	AutoRegister            bool   `json:"auto_register"`
	RequestID               string `json:"request_id"`
}
type Control struct {
	ExpectedSettingsVersion int64  `json:"expected_settings_version"`
	RequestID               string `json:"request_id"`
}
type Repository interface {
	Read(context.Context, string, string) (State, error)
	Save(context.Context, string, string, Input) (State, error)
	Stop(context.Context, string, string, Control) (State, error)
	Resume(context.Context, string, string, Control) (State, error)
	Tick(context.Context, int) (int, error)
}
