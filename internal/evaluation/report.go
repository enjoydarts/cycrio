package evaluation

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
)

var ErrReport = errors.New("invalid synthetic evaluation report")
var reportMonth = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)
var reportCurrency = regexp.MustCompile(`^[A-Z]{3}$`)

func UnverifiedScopes() []string {
	return []string{"real_google_login", "imap_network", "external_ai_accuracy", "external_ai_cost", "hatchet_dispatch_and_storage", "docker_logs", "external_webhooks", "real_user_ui"}
}

type Cost struct {
	Month        string  `json:"month"`
	Timezone     string  `json:"timezone"`
	Currency     *string `json:"currency"`
	KnownAmount  *string `json:"known_amount"`
	Calls        int64   `json:"calls"`
	UnknownCalls int64   `json:"unknown_calls"`
}
type Check struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Scanned int64  `json:"scanned"`
	Passed  bool   `json:"passed"`
}
type Report struct {
	Version           int              `json:"version"`
	Mode              string           `json:"mode"`
	Scope             string           `json:"scope"`
	CostSource        string           `json:"cost_source"`
	StartedAt         time.Time        `json:"started_at"`
	FinishedAt        time.Time        `json:"finished_at"`
	Messages          int64            `json:"messages"`
	Calls             int64            `json:"calls"`
	ExternalCalls     int64            `json:"external_calls"`
	Contracts         int64            `json:"contracts"`
	RegisteredResults int64            `json:"registered_results"`
	BillingEntries    int64            `json:"billing_entries"`
	Outcomes          map[string]int64 `json:"outcomes"`
	DurationsMS       map[string]int64 `json:"durations_ms"`
	Costs             []Cost           `json:"synthetic_costs"`
	Checks            []Check          `json:"checks"`
	NotChecked        []string         `json:"not_checked"`
}

func (r Report) Validate() error {
	if r.Version != 1 || r.Mode != "synthetic" || r.Scope != "normalized_body_to_billing" || r.CostSource != "synthetic_adapter_values" || r.Messages < 1 || r.Messages > 100000 || r.Calls < 0 || r.ExternalCalls != 0 || r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		return ErrReport
	}
	if r.Contracts < 0 || r.RegisteredResults < 0 || r.BillingEntries < 0 || r.Contracts > r.Messages || r.RegisteredResults > r.Messages || r.BillingEntries > r.Messages {
		return ErrReport
	}
	sum := int64(0)
	for state, n := range r.Outcomes {
		if n < 0 || n > r.Messages {
			return ErrReport
		}
		switch state {
		case "succeeded", "not_related", "failed", "unknown":
		default:
			return ErrReport
		}
		sum += n
	}
	if sum != r.Messages {
		return ErrReport
	}
	for _, phase := range []string{"processing", "registration", "billing", "privacy"} {
		n, ok := r.DurationsMS[phase]
		if !ok || n < 0 {
			return ErrReport
		}
	}
	calls := int64(0)
	for _, cost := range r.Costs {
		if !reportMonth.MatchString(cost.Month) || cost.Timezone != "Asia/Tokyo" || cost.Calls < 0 || cost.UnknownCalls < 0 || cost.UnknownCalls > cost.Calls {
			return ErrReport
		}
		if cost.Calls == cost.UnknownCalls {
			if cost.KnownAmount != nil {
				return ErrReport
			}
		} else {
			if cost.Currency == nil || cost.KnownAmount == nil {
				return ErrReport
			}
			if _, e := ai.NormalizeAmount(*cost.KnownAmount); e != nil {
				return ErrReport
			}
		}
		if cost.Currency != nil && !reportCurrency.MatchString(*cost.Currency) {
			return ErrReport
		}
		if cost.Calls > r.Calls-calls {
			return ErrReport
		}
		calls += cost.Calls
	}
	if calls != r.Calls {
		return ErrReport
	}
	required := map[string]bool{"database": false, "job_payload": false, "public_results": false}
	for _, check := range r.Checks {
		if !safeID.MatchString(check.ID) || !safeID.MatchString(check.Scope) || check.Scanned < 1 || !check.Passed {
			return ErrReport
		}
		if _, ok := required[check.ID]; ok {
			required[check.ID] = true
		}
	}
	for _, present := range required {
		if !present {
			return ErrReport
		}
	}
	verified := map[string]bool{}
	for _, scope := range r.NotChecked {
		if !safeID.MatchString(scope) {
			return ErrReport
		}
		verified[scope] = true
	}
	for _, scope := range UnverifiedScopes() {
		if !verified[scope] {
			return ErrReport
		}
	}
	return nil
}
func Write(w io.Writer, r Report) error {
	if r.Validate() != nil || w == nil {
		return ErrReport
	}
	if json.NewEncoder(w).Encode(r) != nil {
		return errors.New("evaluation report write failed")
	}
	return nil
}
