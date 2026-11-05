package priceresearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResearchTraceRedactsJSONEscapedSecrets(t *testing.T) {
	for _, tt := range []struct{ name, key, raw string }{
		{"unicode known key", "secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test"}`},
		{"escaped slash", "secret/for/trace", `{"error":"secret\/for\/trace"}`},
		{"escaped backslash", `secret\for\trace`, `{"error":"\u0073ecret\\for\\trace"}`},
		{"object keys and arrays", "secret-for-trace-test", `{"\u0073ecret-for-trace-test":{"nested":["\u0073ecret-for-trace-test",{"safe":"prefix secret-for-trace-test suffix"}]}}`},
		{"unicode credential prefix", "unrelated", `{"error":"\u0073k-proj-abcdefghijklmno"}`},
		{"unicode router prefix", "unrelated", `["\u0072k-abcdefghijklmno"]`},
		{"unicode remaining credential", "unrelated", `{"error":"sk-proj-\u0061bcdefghijklmno"}`},
		{"duplicate field concealed secret", "secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test","error":"safe"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := maskResearchTrace([]byte(tt.raw), []byte(tt.key))
			if !json.Valid([]byte(got)) || got == tt.raw {
				t.Fatal("escaped credential was not redacted into valid JSON")
			}
			var decoded any
			if err := json.Unmarshal([]byte(got), &decoded); err != nil {
				t.Fatal(err)
			}
			normalized, _ := json.Marshal(decoded)
			if strings.Contains(string(normalized), tt.key) || strings.Contains(string(normalized), "abcdefghijklmno") || strings.Contains(string(normalized), "secret") {
				t.Fatal("decoded JSON still contains a credential")
			}
		})
	}
}

func TestResearchTracePreservesSafeBodiesExactly(t *testing.T) {
	for _, raw := range []string{` { "n": 9007199254740993, "text": "\u0073afe\/text" } `, `not JSON`, `{"incomplete":"safe`, `{"backslash":"\\safe"}`} {
		if got := maskResearchTrace([]byte(raw), []byte("unused-secret")); got != raw {
			t.Fatal("safe body changed")
		}
	}
}

func TestResearchTraceRedactsInvalidJSONEscapedSecrets(t *testing.T) {
	for _, tt := range []struct{ key, raw string }{
		{"secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test"`},
		{"secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test`},
		{"secret/for/trace", `{"error":"secret\/for\/trace`},
		{`secret\for\trace`, `{"error":"\u0073ecret\\for\\trace`},
		{"unrelated", `{"error":"\u0073k-proj-abcdefghijklmno`},
	} {
		got := maskResearchTrace([]byte(tt.raw), []byte(tt.key))
		if got != "[REDACTED]" {
			t.Fatal("invalid JSON with recoverable secret must be wholly redacted")
		}
	}
}

func TestResearchTracePreservesExplicitValidationReason(t *testing.T) {
	quote := quoteFixture()
	out := Result{Models: []Quote{quote}, ModelIssues: map[string]string{quote.ModelID: "price_unverified"}, Trace: &TraceDocument{Validation: map[string]string{quote.ModelID: "verified_official_context"}}}
	finishResearchTrace(&out)
	if out.Trace.Validation[quote.ModelID] != "verified_official_context" {
		t.Fatal("explicit validation reason was overwritten")
	}
}

func TestResearchTraceRedactsNestedEscapedOutputText(t *testing.T) {
	for _, tt := range []struct{ name, key, text string }{
		{"known credential in output JSON", "secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test"}`},
		{"credential pattern in output JSON", "unrelated", `{"error":"\u0073k-proj-abcdefghijklmno"}`},
		{"escaped slash inside output", "secret/for/trace", `{"error":"secret\/for\/trace"}`},
		{"incomplete embedded JSON", "secret-for-trace-test", `{"error":"\u0073ecret-for-trace-test`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, depth := range []int{0, 1, 5, 12} {
				text := tt.text
				for i := 0; i < depth; i++ {
					encoded, _ := json.Marshal(text)
					text = string(encoded)
				}
				raw, _ := json.Marshal(map[string]any{"output": []any{map[string]string{"text": text}}})
				got := maskResearchTrace(raw, []byte(tt.key))
				var decoded struct {
					Output []struct {
						Text string `json:"text"`
					} `json:"output"`
				}
				if json.Unmarshal([]byte(got), &decoded) != nil || len(decoded.Output) != 1 || decoded.Output[0].Text != "[REDACTED]" {
					t.Fatalf("nested output secret retained at depth %d", depth)
				}
			}
		})
	}
}

func TestResearchTraceRedactsNestedEscapedObjectKey(t *testing.T) {
	raw := []byte(`{"{\"error\":\"\\u0073ecret-for-trace-test\"}":"safe"}`)
	got := maskResearchTrace(raw, []byte("secret-for-trace-test"))
	if got != `{"[REDACTED]":"safe"}` {
		t.Fatal("nested escaped object key retained")
	}
}
