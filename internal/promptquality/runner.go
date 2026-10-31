package promptquality

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"reflect"
	"time"
)

type Repository interface {
	State(context.Context) (airuntime.State, error)
	BeginCall(context.Context, airuntime.BeginCallInput) (airuntime.Call, error)
	FinishCall(context.Context, string, airuntime.CallUpdate) (airuntime.Call, error)
}
type Client interface {
	Do(context.Context, ai.Request) (ai.Response, error)
}
type Resolver func(context.Context, string) ([]byte, error)

// Run performs no retries, never accepts external mail, and keeps response facts
// transient. Returned reports contain counts and fixed labels only.
func Run(ctx context.Context, repo Repository, client Client, resolve Resolver, actor string) (report Report, err error) {
	report = NewReport()
	defer func() {
		if err != nil {
			report.Partial = true
			report.StopReason = err.Error()
		}
		report.summarize()
	}()
	if _, e := uuid.Parse(actor); e != nil {
		return report, errors.New("invalid_actor")
	}
	initial, e := repo.State(ctx)
	if e != nil || initial.Active == nil || !initial.ActiveReady {
		return report, errors.New("active_profile_unavailable")
	}
	profile := *initial.Active
	report.Profile = &profile
	report.SettingsVersion = initial.Version
	for _, c := range Corpus() {
		for _, v := range Variants() {
			for _, stage := range []string{"classification", "extraction"} {
				if ctx.Err() != nil {
					return report, errors.New("cancelled")
				}
				if report.CompletedCalls >= MaxPaidCalls {
					return report, errors.New("call_limit")
				}
				current, e := repo.State(ctx)
				if e != nil || !current.ActiveReady || current.Version != initial.Version || !reflect.DeepEqual(current.Active, initial.Active) {
					return report, errors.New("configuration_changed")
				}
				if len(c.Text) > profile.MaxInputBytes {
					return report, errors.New("input_limit")
				}
				model := profile.Primary
				instructions := v.Classifier
				if stage == "extraction" {
					model = profile.Secondary
					instructions = v.Extractor
				}
				key, e := resolve(ctx, model.CredentialID)
				if e != nil || len(key) == 0 {
					clear(key)
					return report, errors.New("credentials_unavailable")
				}
				operation := uuid.NewString()
				call, e := repo.BeginCall(ctx, airuntime.BeginCallInput{OperationID: operation, ProfileID: profile.ID, Stage: stage, AttemptNo: 1, Scope: "admin_test", InitiatedByUserID: &actor})
				if e != nil {
					clear(key)
					return report, errors.New("authorization_or_recording_failed")
				}
				callCtx, cancel := context.WithTimeout(ctx, time.Duration(profile.TimeoutSeconds)*time.Second)
				response, callErr := client.Do(callCtx, ai.Request{Provider: model.Provider, ModelID: model.ModelID, ProviderSlug: profile.OpenRouterProvider, WorkspaceID: profile.AnthropicWorkspaceID, Key: key, MaxOutputTokens: profile.MaxOutputTokens, Price: model.Price, Input: ai.RequestInput{Text: c.Text, Instructions: instructions, ClassificationCriteria: v.Criteria}})
				clear(key)
				timedout := errors.Is(callCtx.Err(), context.DeadlineExceeded)
				cancel()
				outcome, status := outcome(callErr)
				if timedout && callErr != nil {
					outcome = "timeout"
				}
				if outcome == "success" {
					if stage == "classification" && (response.Classification == nil || !validLabel(response.Classification.Label)) {
						outcome = "invalid_output"
					}
					if stage == "extraction" {
						if response.Extraction == nil {
							outcome = "invalid_output"
						} else {
							b := mustExtraction(response.Extraction)
							if _, e := ai.ValidateExtraction(b); e != nil {
								outcome = "invalid_output"
							}
						}
					}
				}
				if response.Cost.Status == "" {
					response.Cost = ai.Cost{Status: "unknown", Reason: "missing_usage"}
				}
				result := Result{CaseID: c.ID, Split: c.Split, Variant: v.Name, Stage: stage, OperationID: operation, CallID: call.ID, Outcome: outcome, Cost: response.Cost}
				if outcome == "success" {
					if stage == "classification" {
						result.Label = response.Classification.Label
					} else {
						s := ScoreEvents(c.Events, response.Extraction.Events)
						result.Events = &s
					}
				}
				report.CompletedCalls++
				if outcome != "success" {
					report.TechnicalErrors++
				}
				report.Results = append(report.Results, result)
				save, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, e = repo.FinishCall(save, call.ID, airuntime.CallUpdate{Outcome: outcome, HTTPStatus: status, GenerationID: response.GenerationID, ActualModelID: response.ActualModelID, ActualProvider: response.ActualProvider, Usage: response.Usage, Cost: response.Cost})
				saveCancel()
				if e != nil {
					return report, errors.New("recording_failed")
				}
				if report.TechnicalErrors >= 3 {
					return report, errors.New("technical_error_limit")
				}
			}
		}
	}
	return report, nil
}
func validLabel(s string) bool {
	return s == "subscription_related" || s == "not_related" || s == "uncertain"
}
func mustExtraction(e *ai.Extraction) []byte {
	if e.Events == nil {
		copy := *e
		copy.Events = []ai.Event{}
		b, _ := json.Marshal(copy)
		return b
	}
	b, _ := json.Marshal(e)
	return b
}
func outcome(err error) (string, int) {
	if err == nil {
		return "success", 0
	}
	var known *ai.Error
	if errors.As(err, &known) {
		switch known.Code {
		case "invalid_request", "transport_error", "request_cancelled", "response_read", "response_too_large", "invalid_json", "provider_http", "provider_error", "model_mismatch", "invalid_output", "refusal", "incomplete", "generation_mismatch":
			return known.Code, known.HTTPStatus
		}
	}
	return "provider_unavailable", 0
}
