package mailprocessing

import (
	"context"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/aibudget"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

type oversizedPipelineRepo struct {
	*pipelineRepo
	inputBytes int
	beginErr   error
}

func (r *oversizedPipelineRepo) BeginOversizedExtraction(ctx context.Context, l Lease, inputBytes int) (airuntime.Call, error) {
	r.inputBytes = inputBytes
	if r.beginErr != nil {
		return airuntime.Call{}, r.beginErr
	}
	return r.pipelineRepo.BeginCall(ctx, l, "extraction")
}

func TestOversizedPrimaryPreservesWholeInputAndRecordsOnlyExtraction(t *testing.T) {
	for _, tc := range []struct {
		name, text, provider, model string
		oversized                   bool
	}{
		{"unverified_d1", strings.Repeat("x", 250001), "liquid", "d1", false},
		{"multibyte", strings.Repeat("あ", 83334), "liquid", "d1.1", true},
		{"d1_1_boundary", strings.Repeat("x", 250000), "liquid", "d1.1", false},
		{"d1_1_over_boundary", strings.Repeat("x", 250001), "liquid", "d1.1", true},
		{"different_provider", strings.Repeat("x", 250001), "other", "d1", false},
		{"unverified_model", strings.Repeat("x", 250001), "liquid", "d1-ultra", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, keys, l := pipelineFixture(t)
			r.w.FullEmail = true
			r.w.Profile.Primary.Provider, r.w.Profile.Primary.ModelID = tc.provider, tc.model
			repo := &oversizedPipelineRepo{pipelineRepo: r}
			calls := 0
			amount, currency := "0.12", "USD"
			reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) { return []byte(tc.text), nil })
			client := inference(func(_ context.Context, q ai.Request) (ai.Response, error) {
				calls++
				if q.Input.Text != tc.text {
					t.Fatal("whole input changed")
				}
				if tc.oversized && (q.Provider != "openai" || q.Input.Instructions != extractorInstructions) {
					t.Fatal("oversized input sent to classifier")
				}
				return ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Extraction: &ai.Extraction{Events: []ai.Event{}}, Usage: ai.Usage{Present: true, Valid: true, InputTokens: 17}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &currency}}, nil
			})
			s := NewService(repo, keys, reader, client, func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
			if err := s.Execute(context.Background(), l); err != nil {
				t.Fatal(err)
			}
			want := 2
			if tc.oversized {
				want = 1
			}
			if calls != want || r.calls != want || r.records != want || r.completion.State != "succeeded" {
				t.Fatal("incorrect calls or result", calls, r.calls, r.records, r.completion)
			}
			if tc.oversized && (repo.inputBytes != len(tc.text) || r.completion.Classification == nil || *r.completion.Classification != "uncertain") {
				t.Fatal("missing honest gate result", repo.inputBytes, r.completion)
			}
			if !tc.oversized && repo.inputBytes != 0 {
				t.Fatal("normal gate bypassed")
			}
			if r.updates[0].Usage.InputTokens != 17 || r.updates[0].Cost.Amount == nil || *r.updates[0].Cost.Amount != amount {
				t.Fatal("extraction accounting changed", r.updates)
			}
		})
	}
}

func TestOversizedExtractionStillRequiresBudgetAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{aibudget.ErrExceeded, "budget_exceeded"}, {aibudget.ErrUnknown, "budget_unknown"}, {ErrNotFound, "configuration_unavailable"}, {ErrConflict, "configuration_unavailable"},
	} {
		r, keys, l := pipelineFixture(t)
		r.w.FullEmail = true
		r.w.Profile.Primary.ModelID = "d1.1"
		repo := &oversizedPipelineRepo{pipelineRepo: r, beginErr: tc.err}
		reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
			return []byte(strings.Repeat("x", 250001)), nil
		})
		s := NewService(repo, keys, reader, inference(func(context.Context, ai.Request) (ai.Response, error) {
			t.Fatal("unauthorized input sent")
			return ai.Response{}, nil
		}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
		if err := s.Execute(context.Background(), l); err != nil || r.completion.ErrorCode != tc.code || r.records != 0 {
			t.Fatal(err, r.completion, r.records)
		}
	}
}

func TestNormalPrimary422DoesNotBypassGate(t *testing.T) {
	r, keys, l := pipelineFixture(t)
	r.w.FullEmail = true
	repo := &oversizedPipelineRepo{pipelineRepo: r}
	s := NewService(repo, keys, textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
		return []byte("short mail"), nil
	}), inference(func(context.Context, ai.Request) (ai.Response, error) {
		return ai.Response{}, &ai.Error{Code: "provider_http", HTTPStatus: 422}
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
	if err := s.Execute(context.Background(), l); err != nil || repo.inputBytes != 0 || r.calls != 1 || r.completion.ErrorCode != "provider_http" {
		t.Fatal(err, r.calls, r.completion)
	}
}
