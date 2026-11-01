package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/google/uuid"
	"sort"
	"strconv"
	"strings"
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
		if errors.Is(err, ErrDispatched) {
			return err
		}
		return s.finish(ctx, l, e, Result{}, "configuration_changed")
	}
	result, err := s.client.Research(ctx, Request{Provider: e.Model.Provider, ModelID: e.Model.ModelID, WorkspaceID: e.WorkspaceID, Key: key, Targets: e.Targets, MaxOutputTokens: 8192})
	code := ""
	if err != nil {
		code = "research_failed"
	}
	return s.finish(ctx, l, e, result, code)
}
func (s *Service) finish(ctx context.Context, l Lease, e Execution, r Result, code string) error {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.repository.Complete(bounded, l, e, r, code)
}
func ValidProvider(p string) bool       { return p == "openai" || p == "anthropic" }
func ValidTargetProvider(p string) bool { return ValidProvider(p) || p == "liquid" }
func Candidates(models []modelcatalog.Model, provider string) []Target {
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
		id := strings.ToLower(m.ModelID)
		excluded := false
		for _, p := range []string{"text-embedding-", "embedding-", "whisper-", "tts-", "dall-e", "gpt-image", "sora-", "gpt-4o-transcribe", "gpt-4o-mini-transcribe", "gpt-4o-mini-tts", "gpt-realtime", "gpt-audio", "omni-moderation", "text-moderation"} {
			if strings.HasPrefix(id, p) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		out = append(out, Target{m.ID, m.Provider, m.ModelID, m.DisplayName})
		if len(out) == 20 {
			break
		}
	}
	return out
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
	if !ValidTargetProvider(q.Provider) || q.ModelID == "" || q.Currency != "USD" || q.Conditions != "standard_global" || q.SourceURL == "" || q.Evidence == "" || q.MaxInputTokens != nil && *q.MaxInputTokens < 1 {
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
