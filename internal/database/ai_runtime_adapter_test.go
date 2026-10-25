//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

type runtimeAdapterTransport func(*http.Request) (*http.Response, error)

func (f runtimeAdapterTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAIRuntimePersistsAnthropicAdapterRefusalUsage(t *testing.T) {
	r, actor, in := runtimeFixture(t)
	ctx := context.Background()
	catalog := NewModelCatalogRepository(r.pool)
	supported := true
	model, err := catalog.AddModel(ctx, actor, modelcatalog.ManualModelInput{Provider: "anthropic", ModelID: "synthetic-anthropic", DisplayName: "Synthetic Anthropic", TextInput: &supported, StructuredOutput: &supported})
	if err != nil {
		t.Fatal(err)
	}
	model, err = catalog.SavePrice(ctx, actor, model.ID, model.Version, modelcatalog.Price{Currency: "USD", RawPricing: json.RawMessage(`{}`), Rates: map[string]modelcatalog.Rate{"input": {Amount: "0.1", Unit: "USD/token"}, "output": {Amount: "0.2", Unit: "USD/token"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCredentialRepository(r.pool).Put(ctx, actor, 0, credentials.CredentialVersion{ID: uuid.NewString(), Provider: "anthropic", Version: 1, Envelope: secretstore.Envelope{KeyID: "synthetic", Ciphertext: []byte("synthetic-encrypted")}}); err != nil {
		t.Fatal(err)
	}
	selected, err := catalog.SaveSettings(ctx, actor, modelcatalog.SettingsInput{ExpectedVersion: 1, PrimaryModelID: "bff14486-c992-44eb-b80f-c599d8edddba", SecondaryModelID: model.ID})
	if err != nil {
		t.Fatal(err)
	}
	in.CatalogSettingsID = *selected.ID
	state, err := r.SaveProfile(ctx, actor, in)
	if err != nil {
		t.Fatal(err)
	}
	call := runtimeCall(t, r, actor, state, "admin_test", nil)
	client := ai.NewClient(&http.Client{Transport: runtimeAdapterTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.anthropic.com/v1/messages" || req.Method != http.MethodPost {
			t.Fatal("unexpected adapter endpoint", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"stop_reason":"end_turn","stop_details":{"type":"refusal","explanation":"private-refusal-content"},"content":[{"type":"text","text":"private-refusal-content"}],"usage":{"input_tokens":4,"output_tokens":1}}`))}, nil
	})})
	response, err := client.Do(ctx, ai.Request{Provider: "anthropic", ModelID: state.Draft.Secondary.ModelID, Key: []byte("synthetic-adapter-secret"), Input: ai.RequestInput{Text: "private-synthetic-mail", Instructions: "private-synthetic-instructions"}, MaxOutputTokens: 100, Price: state.Draft.Secondary.Price})
	var refusal *ai.Error
	if !errors.As(err, &refusal) || refusal.Code != "refusal" || response.Cost.Status != "unknown" || response.Cost.Reason != "refusal_pricing_unsupported" {
		t.Fatal("adapter refusal contract", response, err)
	}
	update := airuntime.CallUpdate{Outcome: refusal.Code, HTTPStatus: refusal.HTTPStatus, Usage: response.Usage, Cost: response.Cost}
	if _, err := r.FinishCall(ctx, call.ID, update); err != nil {
		t.Fatal("adapter refusal usage was not persisted", err)
	}
	call, err = r.GetCall(ctx, call.ID)
	if err != nil || call.Status != "failed" || call.Outcome == nil || *call.Outcome != "refusal" || !call.Usage.Valid || call.Usage.InputTokens != 4 || call.Usage.OutputTokens != 1 || call.Cost.Status != "unknown" || call.Cost.Amount != nil || call.Cost.Reason != "refusal_pricing_unsupported" {
		t.Fatal("persisted refusal metadata", call, err)
	}
	history, err := r.Revisions(ctx, call.ID)
	if err != nil || len(history) != 1 || history[0].Cost.Reason != "refusal_pricing_unsupported" || history[0].Usage != call.Usage {
		t.Fatal("refusal usage revision", history, err)
	}
	raw, _ := json.Marshal(struct {
		Call      airuntime.Call
		Revisions []airuntime.UsageRevision
	}{call, history})
	for _, secret := range []string{"private-refusal-content", "synthetic-adapter-secret", "private-synthetic-mail", "private-synthetic-instructions"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private adapter content persisted")
		}
	}
	update.Cost.Reason = "private-refusal-content"
	if _, err := r.FinishCall(ctx, call.ID, update); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("arbitrary provider reason accepted", err)
	}
}
