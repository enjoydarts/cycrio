package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Service struct {
	repository Repository
	client     Client
	resolve    CredentialResolver
}

func NewService(r Repository, c Client, resolve CredentialResolver) *Service {
	return &Service{r, c, resolve}
}
func (s *Service) State(ctx context.Context) (State, error) { return s.repository.State(ctx) }
func (s *Service) Configure(ctx context.Context, actor string, in ConfigInput) (State, error) {
	if in.ExpectedVersion < 0 || in.IntervalHours < 1 || in.IntervalHours > 168 {
		return State{}, ErrInvalid
	}
	if _, err := uuid.Parse(in.ModelID); err != nil {
		return State{}, ErrInvalid
	}
	return s.repository.Configure(ctx, actor, in)
}
func (s *Service) RequestTest(ctx context.Context, actor string, in RunInput) (Run, error) {
	if in.ExpectedVersion < 0 {
		return Run{}, ErrInvalid
	}
	return s.repository.Request(ctx, actor, "test", in)
}
func (s *Service) RequestUpdate(ctx context.Context, actor string, in RunInput) (Run, error) {
	if in.ExpectedVersion < 0 || !ValidTargetProvider(in.TargetProvider) {
		return Run{}, ErrInvalid
	}
	return s.repository.Request(ctx, actor, "update", in)
}
func (s *Service) Run(ctx context.Context, id string) (Run, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Run{}, ErrInvalid
	}
	return s.repository.Run(ctx, id)
}
func (s *Service) ClaimDue(ctx context.Context) ([]Lease, error) { return s.repository.ClaimDue(ctx) }
func (s *Service) QueueAutomatic(ctx context.Context, p string) error {
	if !ValidTargetProvider(p) {
		return nil
	}
	return s.repository.QueueAutomatic(ctx, p)
}
func (s *Service) Execute(ctx context.Context, l Lease) error {
	e, err := s.repository.Prepare(ctx, l)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.Run.ID != "" && (errors.Is(err, ErrConflict) || errors.Is(err, ErrCredentialsRequired)) {
			code := "configuration_changed"
			if errors.Is(err, ErrCredentialsRequired) {
				code = "credentials_required"
			}
			return s.finish(ctx, l, e, Result{}, code)
		}
		return err
	}
	key, err := s.resolve(ctx, e.CredentialID)
	if err != nil || len(key) == 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.finish(ctx, l, e, Result{}, "credentials_required")
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	// Recheck after resolving the active encrypted version, immediately before sending.
	check, err := s.repository.Prepare(ctx, l)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrDispatched) {
			return err
		}
		return s.finish(ctx, l, e, Result{}, "configuration_changed")
	}
	if check.CredentialID != e.CredentialID || check.Version != e.Version {
		return s.finish(ctx, l, e, Result{}, "configuration_changed")
	}
	if len(e.Targets) == 0 {
		return s.finish(ctx, l, e, Result{}, "")
	}
	if err = s.repository.Begin(ctx, l); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrDispatched) {
			return err
		}
		return s.finish(ctx, l, e, Result{}, "configuration_changed")
	}
	var metadata struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(e.Model.RawMetadata, &metadata)
	outputTokens := 16000
	if limit := e.Model.MaxOutputTokens; limit != nil && *limit > 0 && *limit < int64(outputTokens) {
		outputTokens = int(*limit)
	}
	request := Request{TestOnly: e.Run.Kind == "test", Capabilities: metadata.Capabilities, Provider: e.Model.Provider, ModelID: e.Model.ModelID, WorkspaceID: e.WorkspaceID, Key: key, Targets: e.Targets, MaxOutputTokens: outputTokens}
	if recorder, ok := s.repository.(CallRecorder); ok && e.Run.Kind == "update" {
		indices := make([]int, len(request.Targets))
		for i := range indices {
			indices[i] = i
		}
		if batched, ok := s.repository.(BatchRepository); ok {
			indices, err = batched.BatchIndices(ctx, l)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return s.finish(ctx, l, e, Result{CallsRecorded: true}, "configuration_changed")
			}
			if len(indices) > ResearchBatchSize {
				return s.finish(ctx, l, e, Result{CallsRecorded: true}, "configuration_changed")
			}
		}
		seen := map[int]bool{}
		for _, index := range indices {
			if index < 0 || index >= len(request.Targets) || seen[index] {
				return s.finish(ctx, l, e, Result{CallsRecorded: true}, "configuration_changed")
			}
			seen[index] = true
		}
		if grouped, ok := s.repository.(GroupCallRecorder); ok {
			result, code := s.researchGroup(ctx, l, request, recorder, grouped, indices)
			return s.finish(ctx, l, e, result, code)
		}
		result, code := s.researchModels(ctx, l, request, recorder, indices)
		return s.finish(ctx, l, e, result, code)
	}
	result, err := s.client.Research(ctx, request)
	code := ""
	if err != nil {
		code = "research_failed"
	}
	return s.finish(ctx, l, e, result, code)
}

// CallRecorder keeps each external request separate in the usage ledger.
type CallRecorder interface {
	BeginCall(context.Context, Lease, int) (string, error)
	FinishCall(context.Context, Lease, string, Result, string) error
}

// BatchRepository selects pending items from a persisted, frozen target list.
// The original target index remains the per-call accounting/deduplication key.
const ResearchBatchSize = 16

// GroupCallRecorder reserves a single paid request for several frozen targets.
type GroupCallRecorder interface {
	BeginGroupCall(context.Context, Lease, []int) (string, error)
}

type BatchRepository interface {
	BatchIndices(context.Context, Lease) ([]int, error)
	SkipTarget(context.Context, Lease, int, string) error
}

func (s *Service) researchGroup(ctx context.Context, lease Lease, request Request, recorder CallRecorder, grouped GroupCallRecorder, indices []int) (Result, string) {
	out := Result{CallsRecorded: true}
	if len(indices) == 0 || ctx.Err() != nil {
		return out, ""
	}
	callID, err := grouped.BeginGroupCall(ctx, lease, indices)
	if err != nil {
		if ctx.Err() != nil {
			return out, ""
		}
		return out, "configuration_changed"
	}
	allTargets := request.Targets
	request.Targets = make([]Target, 0, len(indices))
	for _, index := range indices {
		request.Targets = append(request.Targets, allTargets[index])
	}
	result, err := s.client.Research(ctx, request)
	code := ""
	if err != nil {
		code = "research_failed"
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if recorder.FinishCall(bounded, lease, callID, result, code) != nil {
		code = "research_failed"
	}
	result.CallsRecorded = true
	return result, code
}

func (s *Service) researchModels(ctx context.Context, lease Lease, request Request, recorder CallRecorder, indices []int) (Result, string) {
	results := make([]Result, len(indices))
	codes := make([]string, len(indices))
	for i := range codes {
		codes[i] = "not_started"
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for n := 0; n < 4; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for position := range jobs {
				if ctx.Err() != nil {
					continue
				}
				index := indices[position]
				callID, err := recorder.BeginCall(ctx, lease, index)
				if err != nil {
					if ctx.Err() != nil {
						continue
					}
					codes[position] = "configuration_changed"
					if batched, ok := s.repository.(BatchRepository); ok {
						bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
						_ = batched.SkipTarget(bounded, lease, index, "configuration_changed")
						cancel()
					}
					continue
				}
				single := request
				single.Targets = []Target{request.Targets[index]}
				codes[position] = ""
				results[position], err = s.client.Research(ctx, single)
				if err != nil {
					codes[position] = "research_failed"
				}
				bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				if recorder.FinishCall(bounded, lease, callID, results[position], codes[position]) != nil {
					codes[position] = "research_failed"
				}
				cancel()
			}
		}()
	}
feed:
	for position := range indices {
		select {
		case jobs <- position:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	workers.Wait()
	out := Result{CallsRecorded: true}
	succeeded := 0
	for index, result := range results {
		out.SearchCalls += result.SearchCalls
		if codes[index] == "" {
			succeeded++
			out.Quotes = append(out.Quotes, result.Quotes...)
			out.Sources = append(out.Sources, result.Sources...)
		}
	}
	if succeeded == 0 && len(indices) > 0 && ctx.Err() == nil {
		return out, "research_failed"
	}
	return out, ""
}
func (s *Service) finish(ctx context.Context, l Lease, e Execution, r Result, code string) error {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.repository.Complete(bounded, l, e, r, code)
}
func ValidProvider(p string) bool       { return p == "openai" || p == "anthropic" }
func ValidTargetProvider(p string) bool { return ValidProvider(p) || p == "liquid" }
func Candidates(models []modelcatalog.Model, provider string) []Target {
	if !ValidTargetProvider(provider) {
		return []Target{}
	}
	sort.SliceStable(models, func(i, j int) bool {
		a, b := created(models[i]), created(models[j])
		if a != b {
			return a > b
		}
		return models[i].ID < models[j].ID
	})
	out := []Target{}
	for _, m := range models {
		if m.Provider != provider || !m.Enabled || !m.Available || m.DeletedAt != nil || m.TextInput != nil && !*m.TextInput {
			continue
		}
		out = append(out, Target{m.ID, m.Provider, m.ModelID, m.DisplayName})
	}
	return groupResearchFamilies(out)
}
func created(m modelcatalog.Model) float64 {
	var v map[string]any
	if json.Unmarshal(m.RawMetadata, &v) != nil {
		return 0
	}
	for _, k := range []string{"created", "created_at", "release_date"} {
		if n, ok := v[k].(float64); ok {
			return n
		}
		if str, ok := v[k].(string); ok {
			if t, err := time.Parse(time.RFC3339, str); err == nil {
				return float64(t.Unix())
			}
			if t, err := time.Parse("2006-01-02", str); err == nil {
				return float64(t.Unix())
			}
		}
	}
	return 0
}
func QuotePrice(q Quote) (modelcatalog.Price, error) {
	p := modelcatalog.Price{Source: "research", Currency: q.Currency, SourceURL: q.SourceURL, Rates: map[string]modelcatalog.Rate{}}
	if !ValidTargetProvider(q.Provider) || q.ModelID == "" || q.Currency != "USD" || q.Conditions != "standard_global" || q.SourceURL == "" || q.MaxInputTokens != nil && (*q.MaxInputTokens < 1 || *q.MaxInputTokens > 1000000000) {
		return p, ErrInvalid
	}
	canonical, ok := officialURL(q.SourceURL, q.Provider)
	if !ok || len(q.Evidence) > 8192 {
		return p, ErrInvalid
	}
	p.SourceURL = canonical
	amounts := map[string]*string{"input": &q.InputPerMillion, "output": &q.OutputPerMillion, "cache_read": q.CacheReadPerMillion, "cache_write": q.CacheWritePerMillion, "cache_write_30m": q.CacheWrite30mPerMillion, "cache_write_1h": q.CacheWrite1hPerMillion, "cache_write_12h": q.CacheWrite12hPerMillion}
	for name, v := range amounts {
		if v == nil {
			continue
		}
		n, err := modelcatalog.PerMillion(*v)
		if err != nil {
			return p, ErrInvalid
		}
		p.Rates[name] = modelcatalog.Rate{Amount: n, Unit: "USD/token"}
	}
	metadata := map[string]string{"research_version": "v1", "pricing_scope": "standard_global", "source": q.SourceURL, "evidence": q.Evidence}
	if q.MaxInputTokens != nil {
		metadata["max_input_tokens"] = strconv.FormatInt(*q.MaxInputTokens, 10)
	}
	p.RawPricing, _ = json.Marshal(metadata)
	return p, nil
}
