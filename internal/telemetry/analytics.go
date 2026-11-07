package telemetry

import (
	"context"
	"math"
	"regexp"
	"time"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

// Analytics exposes only typed technical measurements, never arbitrary properties.
type Analytics interface {
	TrackAPI(string, string, int, time.Duration)
	TrackJob(string, string, string, time.Duration)
	TrackAI(string, AIObservation)
}

type analyticsKey struct{}

func WithAnalytics(ctx context.Context, reporter Analytics) context.Context {
	if r, ok := reporter.(*Reporter); ok && (r == nil || !r.production) {
		return ctx
	}
	return context.WithValue(ctx, analyticsKey{}, reporter)
}

func HasAnalytics(ctx context.Context) bool {
	_, ok := ctx.Value(analyticsKey{}).(Analytics)
	return ok
}

func RecordAI(ctx context.Context, owner string, observation AIObservation) {
	defer containPanic()
	if r, ok := ctx.Value(analyticsKey{}).(Analytics); ok {
		r.TrackAI(owner, observation)
	}
}

type AIObservation struct {
	CallID, TraceID, Provider, Model, Stage, Scope, State, CostStatus string
	InputTokens, OutputTokens                                         *int64
	CostUSD                                                           *float64
	Duration                                                          time.Duration
	FinishedAt                                                        time.Time
}

func validOwner(owner string) bool {
	if owner == "cycrio-system" {
		return true
	}
	id, err := uuid.Parse(owner)
	return err == nil && id != uuid.Nil && id.String() == owner
}

func validDuration(d time.Duration) bool { return d >= 0 && d <= 24*time.Hour }

func (r *Reporter) track(owner, name, id string, at time.Time, properties posthog.Properties) {
	defer containPanic()
	if r == nil || !r.production || !validOwner(owner) {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || !r.config.valid() {
		return
	}
	properties["runtime"] = r.runtime
	properties["$process_person_profile"] = false
	properties["$geoip_disable"] = true
	if at.IsZero() {
		at = time.Now().UTC()
	}
	select {
	case r.analyticsQueue <- event{owner: owner, name: name, id: id, at: at, version: r.version, properties: properties}:
	default:
	}
}

func validAction(action string) bool {
	switch action {
	case "session_check", "overview_read", "subscriptions_read", "candidates_read", "analysis_read", "mail_account_save", "mail_probe_request", "mail_import_request", "mail_processing_request", "subscription_create", "subscription_update", "subscription_source_attach", "candidate_dismiss":
		return true
	}
	return false
}

func (r *Reporter) TrackAPI(owner, action string, status int, duration time.Duration) {
	if !validAction(action) || status < 200 || status > 599 || !validDuration(duration) {
		return
	}
	state := "succeeded"
	if status >= 500 {
		state = "failed"
	} else if status >= 300 {
		state = "rejected"
	}
	r.track(owner, "cycrio_api_operation", "", time.Time{}, posthog.Properties{"action": action, "state": state, "status_code": status, "duration_ms": float64(duration) / float64(time.Millisecond)})
}

func (r *Reporter) TrackJob(owner, operation, state string, duration time.Duration) {
	if !allowedOperation(operation) || operation == "api_request" || !validDuration(duration) {
		return
	}
	switch state {
	case "succeeded", "failed", "unknown", "returned", "cancelled", "stopped", "skipped":
	default:
		return
	}
	if owner == "" {
		owner = "cycrio-system"
	}
	r.track(owner, "cycrio_job_completed", "", time.Time{}, posthog.Properties{"operation": operation, "state": state, "duration_ms": float64(duration) / float64(time.Millisecond)})
}

func (r *Reporter) TrackQueue(queue string, queued, active int64, oldestAgeSeconds float64) {
	if queue != "mail_sync" && queue != "mail_processing" || queued < 0 || active < 0 || oldestAgeSeconds < 0 || math.IsNaN(oldestAgeSeconds) || math.IsInf(oldestAgeSeconds, 0) {
		return
	}
	r.track("cycrio-system", "cycrio_queue_snapshot", "", time.Time{}, posthog.Properties{"queue": queue, "queued_runs": queued, "active_runs": active, "oldest_age_seconds": oldestAgeSeconds})
}

var analyticsModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,255}$`)

func (r *Reporter) TrackAI(owner string, v AIObservation) {
	defer containPanic()
	if !validOwner(v.CallID) || v.CallID == "cycrio-system" || !validDuration(v.Duration) || !analyticsModel.MatchString(v.Model) || diagnostics.ScrubText(v.Model) != v.Model {
		return
	}
	switch v.Provider {
	case "liquid", "openai", "openrouter", "anthropic":
	default:
		return
	}
	switch v.Stage {
	case "classification", "extraction", "match", "repair", "price_research":
	default:
		return
	}
	switch v.Scope {
	case "user", "admin_test", "system":
	default:
		return
	}
	switch v.State {
	case "succeeded", "failed", "unknown":
	default:
		return
	}
	switch v.CostStatus {
	case "estimated", "provider_reported", "unknown":
	default:
		return
	}
	trace := v.CallID
	if validOwner(v.TraceID) && v.TraceID != "cycrio-system" {
		trace = v.TraceID
	}
	trace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("cycrio-trace/"+trace)).String()
	properties := posthog.Properties{"$ai_model": v.Model, "$ai_provider": v.Provider, "$ai_trace_id": trace, "$ai_session_id": nil, "$ai_latency": v.Duration.Seconds(), "$ai_is_error": v.State != "succeeded", "scope": v.Scope, "stage": v.Stage, "state": v.State, "cost_status": v.CostStatus, "cost_known": false, "usage_known": false}
	if v.InputTokens != nil && v.OutputTokens != nil && *v.InputTokens >= 0 && *v.OutputTokens >= 0 && *v.InputTokens <= 3000000000 && *v.OutputTokens <= 3000000000 {
		properties["$ai_input_tokens"], properties["$ai_output_tokens"] = *v.InputTokens, *v.OutputTokens
		properties["usage_known"] = true
	}
	if v.CostStatus != "unknown" && v.CostUSD != nil && !math.IsNaN(*v.CostUSD) && !math.IsInf(*v.CostUSD, 0) && *v.CostUSD >= 0 {
		properties["$ai_total_cost_usd"], properties["cost_usd"] = *v.CostUSD, *v.CostUSD
		properties["cost_known"] = true
	}
	// The ledger call UUID is not a mail identifier. Replays share this UUID.
	r.track(owner, "$ai_generation", uuid.NewSHA1(uuid.NameSpaceOID, []byte("cycrio-ai/"+v.CallID)).String(), v.FinishedAt, properties)
}
