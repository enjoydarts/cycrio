package priceresearch

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"strings"
	"testing"
)

func TestCandidateSelection(t *testing.T) {
	no := false
	models := []modelcatalog.Model{{ID: "2", Provider: "openai", ModelID: "new-model", Available: true, Enabled: true}, {ID: "1", Provider: "openai", ModelID: "text-embedding-3", Available: true, Enabled: true}, {ID: "3", Provider: "anthropic", ModelID: "claude-next", Available: true, Enabled: true}, {ID: "4", Provider: "openai", ModelID: "disabled", Available: true}, {ID: "5", Provider: "openai", ModelID: "explicit", TextInput: &no, Available: true, Enabled: true}}
	got := Candidates(models, "openai")
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("candidates: %+v", got)
	}
}
func TestCandidateSelectionKeepsUnknownNamesAndLatestRelease(t *testing.T) {
	models := []modelcatalog.Model{{ID: "a", Provider: "liquid", ModelID: "future-audio-text", Available: true, Enabled: true, RawMetadata: json.RawMessage(`{"release_date":"2026-01-01"}`)}, {ID: "z", Provider: "liquid", ModelID: "future-new", Available: true, Enabled: true, RawMetadata: json.RawMessage(`{"release_date":"2026-10-01"}`)}}
	got := Candidates(models, "liquid")
	if len(got) != 2 || got[0].ID != "z" {
		t.Fatal(got)
	}
}
func TestQuotePriceUnknownAndMetadata(t *testing.T) {
	q := Quote{ModelID: "next", Provider: "openai", Currency: "USD", InputPerMillion: "1.25", OutputPerMillion: "5", SourceURL: "https://openai.com/api/pricing", Evidence: "next input 1.25 output 5", Conditions: "standard_global"}
	p, err := QuotePrice(q)
	if err != nil || p.Source != "research" || !strings.Contains(string(p.RawPricing), `"pricing_scope":"standard_global"`) {
		t.Fatalf("price %+v %v", p, err)
	}
	for _, bad := range []Quote{{}, {Currency: "USD", InputPerMillion: "-1", OutputPerMillion: "5", Conditions: "standard_global"}} {
		if _, err := QuotePrice(bad); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	bad := q
	bad.SourceURL = "https://attacker.example/pricing"
	if _, err := QuotePrice(bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("unofficial source accepted", err)
	}
	raw, _ := json.Marshal(Request{Key: []byte("SECRET")})
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("secret exposed")
	}
}

type fakeRepository struct {
	Repository
	execution Execution
	completed Result
	code      string
}

func (f *fakeRepository) Prepare(context.Context, Lease) (Execution, error) { return f.execution, nil }
func (f *fakeRepository) Begin(context.Context, Lease) error                { return nil }
func (f *fakeRepository) Complete(_ context.Context, _ Lease, _ Execution, r Result, c string) error {
	f.completed = r
	f.code = c
	return nil
}

type failingClient struct{}

func (failingClient) Research(context.Context, Request) (Result, error) {
	return Result{SearchCalls: 1}, errors.New("secret body")
}
func TestExecuteSanitizesAndRecordsFailedCall(t *testing.T) {
	r := &fakeRepository{execution: Execution{CredentialID: "credential", Model: modelcatalog.Model{Provider: "openai"}, Targets: []Target{{ModelID: "target"}}}}
	s := NewService(r, failingClient{}, func(context.Context, string) ([]byte, error) { return []byte("secret"), nil })
	if err := s.Execute(context.Background(), Lease{}); err != nil {
		t.Fatal(err)
	}
	if r.code != "research_failed" || r.completed.SearchCalls != 1 {
		t.Fatalf("%+v %s", r.completed, r.code)
	}
}
