package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type serviceRepository struct {
	Repository
	saved           Price
	applied         []CatalogEntry
	finished        string
	invalidLease    bool
	deadlineMissing bool
}

func (r *serviceRepository) List(context.Context, Filter) (ListResult, error) {
	return ListResult{Models: []Model{}}, nil
}

func (r *serviceRepository) SavePrice(_ context.Context, _, _ string, _ int64, p Price) (Model, error) {
	r.saved = p
	return Model{ManualPrice: &p}, nil
}
func (r *serviceRepository) ValidateLease(ctx context.Context, _ string, _ string) error {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 60*time.Second {
		r.deadlineMissing = true
	}
	if r.invalidLease {
		return ErrConflict
	}
	return nil
}
func (r *serviceRepository) SyncStates(context.Context) ([]SyncState, error) {
	return []SyncState{{Provider: "openrouter"}}, nil
}
func (r *serviceRepository) ApplySync(ctx context.Context, _, _ string, items []CatalogEntry) error {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 60*time.Second {
		r.deadlineMissing = true
	}
	r.applied = items
	return nil
}
func (r *serviceRepository) FinishSync(_ context.Context, _, _, code string) error {
	r.finished = code
	return nil
}
func ptr(s string) *string { return &s }

func TestServiceManualCacheWriteTTLsRemainDistinctAndExact(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       map[string]Rate
		absent     string
	}{
		{"zero_30m_unknown_12h", `{"currency":"USD","cache_write_30m_per_million":"0","effective_from":"2026-10-25T00:00:00Z","source_url":"https://example.com/prices"}`, map[string]Rate{"cache_write_30m": {Amount: "0", Unit: "USD/token"}}, "cache_write_12h"},
		{"exact_both_ttls", `{"currency":"JPY","cache_write_30m_per_million":"1.234567890123456789012345","cache_write_12h_per_million":"2.5","effective_from":"2026-10-25T00:00:00Z","source_url":"https://example.com/prices"}`, map[string]Rate{"cache_write_30m": {Amount: "0.000001234567890123456789012345", Unit: "JPY/token"}, "cache_write_12h": {Amount: "0.0000025", Unit: "JPY/token"}}, "cache_write"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var in ManualPriceInput
			if err := json.Unmarshal([]byte(tt.body), &in); err != nil {
				t.Fatal(err)
			}
			repo := &serviceRepository{}
			s := NewService(repo, nil)
			if _, err := s.SavePrice(context.Background(), uuid.NewString(), uuid.NewString(), in); err != nil {
				t.Fatal(err)
			}
			for key, want := range tt.want {
				if got := repo.saved.Rates[key]; got != want {
					t.Fatal(key, got, want)
				}
			}
			if _, ok := repo.saved.Rates[tt.absent]; ok {
				t.Fatal("unspecified TTL became a known rate", tt.absent)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(repo.saved.RawPricing, &raw); err != nil {
				t.Fatal(err)
			}
			for key := range tt.want {
				if _, ok := raw[key+"_per_million"]; !ok {
					t.Fatal("TTL lost from raw input", key)
				}
			}
		})
	}
	for _, field := range []string{"cache_write_30m_per_million", "cache_write_12h_per_million"} {
		var in ManualPriceInput
		json.Unmarshal([]byte(`{"currency":"USD","`+field+`":"-1","effective_from":"2026-10-25T00:00:00Z","source_url":"https://example.com/prices"}`), &in)
		if _, err := NewService(&serviceRepository{}, nil).SavePrice(context.Background(), uuid.NewString(), uuid.NewString(), in); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid TTL rate accepted", field, err)
		}
	}
}

func TestServiceManualMoneyAndInputValidation(t *testing.T) {
	repo := &serviceRepository{}
	s := NewService(repo, nil)
	id := uuid.NewString()
	actor := uuid.NewString()
	if _, err := s.GetModel(context.Background(), "not-a-uuid"); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid model identifier", err)
	}
	if _, err := s.List(context.Background(), Filter{Search: strings.Repeat("a", 101)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("long search accepted", err)
	}
	when := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	in := ManualPriceInput{Currency: "USD", InputPerMillion: ptr("0"), OutputPerMillion: ptr("1.25"), PerRequest: ptr("0.001"), SourceURL: "https://example.com/prices", EffectiveFrom: &when}
	_, err := s.SavePrice(context.Background(), actor, id, in)
	if err != nil || repo.saved.Rates["input"].Amount != "0" || repo.saved.Rates["output"].Amount != "0.00000125" || repo.saved.Rates["request"].Unit != "USD/request" {
		t.Fatal(repo.saved, err)
	}
	for _, mutate := range []func(*ManualPriceInput){func(v *ManualPriceInput) { v.Currency = "usd" }, func(v *ManualPriceInput) { v.Currency = "XXX" }, func(v *ManualPriceInput) { v.InputPerMillion = ptr("-1") }, func(v *ManualPriceInput) { v.SourceURL = "http://example.com" }, func(v *ManualPriceInput) { v.SourceURL = "https://user:secret@example.com" }, func(v *ManualPriceInput) { v.ExpectedVersion = -1 }} {
		v := in
		mutate(&v)
		if _, err := s.SavePrice(context.Background(), actor, id, v); !errors.Is(err, ErrInvalid) {
			t.Fatal(v, err)
		}
	}
	for _, mutate := range []func(*ManualPriceInput){func(v *ManualPriceInput) { v.EffectiveFrom = nil }, func(v *ManualPriceInput) { v.SourceURL = "" }} {
		v := in
		mutate(&v)
		if _, err := s.SavePrice(context.Background(), actor, id, v); !errors.Is(err, ErrInvalid) {
			t.Fatal("missing price provenance accepted", err)
		}
	}
	if _, err := s.SavePrice(context.Background(), actor, id, ManualPriceInput{Currency: "USD"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty pricing", err)
	}
	for _, v := range []ManualModelInput{{Provider: "resend", ModelID: "x", DisplayName: "X"}, {Provider: "openai", ModelID: "x\n", DisplayName: "X"}, {Provider: "openai", ModelID: "x", DisplayName: ""}} {
		if _, err := s.AddModel(context.Background(), actor, v); !errors.Is(err, ErrInvalid) {
			t.Fatal(v, err)
		}
	}
	if _, err := s.ConfigureSync(context.Background(), actor, "anthropic", SyncConfigInput{IntervalHours: 24, WorkspaceID: "bad\r\n"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.ConfigureSync(context.Background(), actor, "openai", SyncConfigInput{IntervalHours: 24, WorkspaceID: "workspace"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.ConfigureSync(context.Background(), actor, "liquid", SyncConfigInput{IntervalHours: 24, AutomaticEnabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestServiceSyncDoesNotApplyPartialAndChecksLease(t *testing.T) {
	r := &serviceRepository{}
	f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(`{"data":[{"id":"x"},{"id":"x"}]}`), nil }), nil)
	s := NewService(r, f)
	if err := s.SyncProvider(context.Background(), "openrouter", uuid.NewString()); !errors.Is(err, ErrUnavailable) || r.applied != nil || r.finished != "provider_unavailable" {
		t.Fatal(err, r)
	}
	r.invalidLease = true
	r.finished = ""
	if err := s.SyncProvider(context.Background(), "openrouter", uuid.NewString()); !errors.Is(err, ErrConflict) || r.finished != "" {
		t.Fatal(err, r)
	}
	r.invalidLease = false
	f = NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(`{"data":[{"id":"x","name":"X"}]}`), nil }), nil)
	s = NewService(r, f)
	if err := s.SyncProvider(context.Background(), "openrouter", uuid.NewString()); err != nil || len(r.applied) != 1 {
		t.Fatal(err)
	}
	if r.deadlineMissing {
		t.Fatal("database operations not bounded by total sync deadline")
	}
	_, err := json.Marshal(r.applied)
	if err != nil {
		t.Fatal(err)
	}
}
