package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsWirePreservesOnlyTypedMetadata(t *testing.T) {
	wire := make(chan []byte, 8)
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, func(ctx context.Context, owner string, send func(context.Context) error) error { return send(ctx) }, "worker", transportFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		wire <- b
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	}), time.Hour)
	defer r.Close()
	waitConfig(t, r)
	in, out, amount := int64(123), int64(45), 0.002
	observation := AIObservation{CallID: "4a5ae6e2-b2e5-42ef-9cd6-f6ab158687be", TraceID: "ea843a27-2845-4ea1-84f7-57b14d239373", Provider: "openai", Model: "gpt-5-mini", Stage: "extraction", Scope: "user", State: "succeeded", CostStatus: "estimated", InputTokens: &in, OutputTokens: &out, CostUSD: &amount, Duration: 1500 * time.Millisecond}
	r.TrackAI(testOwner, observation)
	read := func() map[string]any {
		t.Helper()
		select {
		case b := <-wire:
			for _, forbidden := range []string{"$ip", "$host", "$ai_input\"", "$ai_output_choices", "credential", "email", "/Users/"} {
				if strings.Contains(string(b), forbidden) {
					t.Fatalf("private field %s: %s", forbidden, b)
				}
			}
			var env struct {
				Batch []map[string]any `json:"batch"`
			}
			if err := json.Unmarshal(b, &env); err != nil || len(env.Batch) != 1 {
				t.Fatalf("invalid wire %s", b)
			}
			return env.Batch[0]
		case <-time.After(3 * time.Second):
			t.Fatal("analytics event not delivered")
			return nil
		}
	}
	first := read()
	props := first["properties"].(map[string]any)
	if props["distinct_id"] != testOwner {
		t.Fatalf("capture lost the required trusted identity: %v", first)
	}
	if first["event"] != "$ai_generation" || props["$ai_input_tokens"] != float64(123) || props["$ai_total_cost_usd"] != amount || props["$ai_latency"] != 1.5 || props["$process_person_profile"] != false {
		t.Fatal(first)
	}
	// Retries retain a deterministic event UUID, so ingestion can deduplicate.
	r.TrackAI(testOwner, observation)
	if second := read(); first["uuid"] == nil || first["uuid"] != second["uuid"] {
		t.Fatal(first, second)
	}
	observation.InputTokens, observation.OutputTokens, observation.CostUSD = nil, nil, nil
	observation.CostStatus = "unknown"
	r.TrackAI(testOwner, observation)
	props = read()["properties"].(map[string]any)
	for _, key := range []string{"$ai_input_tokens", "$ai_output_tokens", "$ai_total_cost_usd"} {
		if _, ok := props[key]; ok {
			t.Fatalf("unknown replaced with zero: %s", key)
		}
	}
	if props["cost_known"] != false {
		t.Fatal(props)
	}
	r.TrackJob(testOwner, "worker_mail_processing", "unknown", 250*time.Millisecond)
	if got := read(); got["event"] != "cycrio_job_completed" || got["properties"].(map[string]any)["state"] != "unknown" {
		t.Fatal(got)
	}
	// Repeated legitimate successful operations must not be exception-deduplicated.
	for i := 0; i < 2; i++ {
		r.TrackAPI(testOwner, "subscription_create", 201, time.Millisecond)
		if got := read(); got["event"] != "cycrio_api_operation" {
			t.Fatal(got)
		}
	}
	r.TrackQueue("mail_processing", 2, 1, 120)
	queued := read()
	if queued["event"] != "cycrio_queue_snapshot" || queued["properties"].(map[string]any)["distinct_id"] != "cycrio-system" {
		t.Fatalf("system measurement lost identity: %v", queued)
	}
}

func TestAnalyticsRejectsUntrustedDimensionsAndDevelopment(t *testing.T) {
	r := &Reporter{production: true, config: testConfig, analyticsQueue: make(chan event, 8)}
	r.TrackAPI("private@example.com", "subscription_create", 201, time.Second)
	r.TrackAPI(testOwner, "private@example.com", 201, time.Second)
	r.TrackJob(testOwner, "worker_mail_processing", "private@example.com", time.Second)
	r.TrackAI(testOwner, AIObservation{CallID: testOwner, Provider: "openai", Model: "private@example.com"})
	if len(r.analyticsQueue) != 0 {
		t.Fatal("untrusted metadata accepted")
	}
	r.production = false
	r.TrackAPI(testOwner, "subscription_create", 201, time.Second)
	if len(r.analyticsQueue) != 0 {
		t.Fatal("development event accepted")
	}
}
