package mailprocessing

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"strings"
	"testing"
)

func TestFullEmailPipelineSendsSameWholeBodyToBothStages(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, media := range []string{"text/plain", "text/html"} {
			r, k, l := pipelineFixture(t)
			r.w.FullEmail = full
			r.w.Profile.MaxInputBytes = 8192
			want := strings.Repeat("日本語", 60000) + "末尾確認"
			calls := 0
			reader := textReader(func(_ context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
				if source.FullEmail != full {
					t.Fatal("reader mode lost")
				}
				raw := want
				if media == "text/html" {
					raw = "<p>" + want + "</p>"
				}
				return mailcontent.NormalizeSource([]byte(raw), media, "utf-8", "8bit", source)
			})
			client := inference(func(_ context.Context, q ai.Request) (ai.Response, error) {
				calls++
				if q.Input.Text != want {
					t.Fatal("body changed")
				}
				return ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Extraction: &ai.Extraction{Events: []ai.Event{}}}, nil
			})
			s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic"), nil })
			if err := s.Execute(context.Background(), l); err != nil {
				t.Fatal(err)
			}
			if full {
				if calls != 2 || r.completion.State != "succeeded" {
					t.Fatal("whole email blocked", calls, r.completion)
				}
			} else if calls != 0 || r.completion.ErrorCode != "input_too_large" {
				t.Fatal("legacy changed", calls, r.completion)
			}
		}
	}
}

func TestFullEmailPipelineStillRejectsPhysicalOverflowAndInvalidUTF8BeforeAI(t *testing.T) {
	for _, tc := range []struct {
		text []byte
		code string
	}{
		{[]byte(strings.Repeat("x", mailcontent.MaxWireBytes+1)), "input_too_large"},
		{[]byte{255}, "invalid_response"},
	} {
		r, k, l := pipelineFixture(t)
		r.w.FullEmail = true
		reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
			return append([]byte(nil), tc.text...), nil
		})
		s := NewService(r, k, reader, nil, nil)
		if err := s.Execute(context.Background(), l); err != nil || r.calls != 0 || r.completion.ErrorCode != tc.code {
			t.Fatal("invalid whole input called AI", r.calls, r.completion, err)
		}
	}
}

func TestFullEmailRequestSurvivesSerialization(t *testing.T) {
	var in Input
	if err := json.Unmarshal([]byte(`{"full_email":true}`), &in); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(in)
	if !strings.Contains(string(raw), `"full_email":true`) {
		t.Fatal("explicit whole-email mode lost", string(raw))
	}
	var old Input
	raw, _ = json.Marshal(old)
	if strings.Contains(string(raw), "full_email") {
		t.Fatal("legacy request document changed", string(raw))
	}
}

func TestStandardProofRejectsFullEmailScopeMismatchBeforeCustomReturn(t *testing.T) {
	for _, mode := range []string{"custom", "standard"} {
		var p Policy
		raw := `{"profile_id":"p","version":1,"full_email_allowed":true,"standard_proof":{"profile_id":"p","policy_version":1,"primary_mode":"` + mode + `","primary_terms":{"id":"` + CurrentPrimaryTerms().ID + `","version":"` + CurrentPrimaryTerms().Version + `"}}}`
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		if StandardProofCurrent(p) {
			t.Fatal("mismatched full-email scope authorized", mode)
		}
	}
}

func TestLegacyProofScopeDefaultsBoundedAndNilCustomProofUsesAdminColumn(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{"profile_id":"p","version":1,"standard_proof":{"profile_id":"p","policy_version":1,"primary_mode":"custom"}}`), &p); err != nil || !StandardProofCurrent(p) {
		t.Fatal("legacy proof no longer compatible", err)
	}
	p.StandardProof = nil
	p.FullEmailAllowed = true
	if !StandardProofCurrent(p) {
		t.Fatal("explicit administrator legacy-custom scope requires invented proof")
	}
}
