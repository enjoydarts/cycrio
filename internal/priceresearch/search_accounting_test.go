package priceresearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestInspectOpenAISearchUsage(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		limit   int
		count   int
		known   bool
		actions []SearchAction
	}{
		{"completed search with unfinished navigation", `[{"type":"web_search_call","id":"s","status":"completed","action":{"type":"search","query":"private query"}},{"type":"web_search_call","status":"in_progress","action":{"type":"open_page","url":"https://private.example"}},{"type":"web_search_call","status":"failed","action":{"type":"find_in_page"}}]`, 2, 1, true, []SearchAction{{"search", "completed"}, {"open_page", "in_progress"}, {"find_in_page", "failed"}}},
		{"partial search below cap", `[{"type":"web_search_call","status":"completed","action":{"type":"search"}},{"type":"web_search_call","status":"in_progress","action":{"type":"search"}}]`, 2, 1, false, []SearchAction{{"search", "completed"}, {"search", "in_progress"}}},
		{"failed search below cap", `[{"type":"web_search_call","status":"failed","action":{"type":"search"}}]`, 2, 0, false, []SearchAction{{"search", "failed"}}},
		{"cap proves total despite partial search", `[{"type":"web_search_call","status":"completed","action":{"type":"search"}},{"type":"web_search_call","status":"in_progress","action":{"type":"search"}}]`, 1, 1, true, []SearchAction{{"search", "completed"}, {"search", "in_progress"}}},
		{"cap proves total despite unknown action", `[{"type":"web_search_call","status":"completed","action":{"type":"search"}},{"type":"web_search_call","status":"completed","action":{"type":"private arbitrary action"}}]`, 1, 1, true, []SearchAction{{"search", "completed"}, {"unknown", "completed"}}},
		{"actual total is not clamped", `[{"type":"web_search_call","status":"completed","action":{"type":"search"}},{"type":"web_search_call","status":"completed","action":{"type":"search"}}]`, 1, 2, true, []SearchAction{{"search", "completed"}, {"search", "completed"}}},
		{"empty output", `[]`, 2, 0, true, nil},
		{"non search output", `[{"type":"message","content":[]}]`, 2, 0, true, nil},
		{"missing output", ``, 2, 0, false, nil},
		{"null output", `null`, 2, 0, false, nil},
		{"invalid json", `[`, 2, 0, false, nil},
		{"object output", `{}`, 2, 0, false, nil},
		{"malformed item", `[null]`, 2, 0, false, nil},
		{"missing item type", `[{}]`, 2, 0, false, nil},
		{"duplicate id", `[{"type":"web_search_call","id":"same","status":"completed","action":{"type":"search"}},{"type":"web_search_call","id":"same","status":"completed","action":{"type":"search"}}]`, 2, 1, true, []SearchAction{{"search", "completed"}}},
		{"conflicting id", `[{"type":"web_search_call","id":"same","status":"completed","action":{"type":"search"}},{"type":"web_search_call","id":"same","status":"failed","action":{"type":"search"}}]`, 1, 1, false, []SearchAction{{"search", "completed"}, {"search", "failed"}}},
		{"unknown status sanitized", `[{"type":"web_search_call","status":"private-status","action":{"type":"search"}}]`, 2, 0, false, []SearchAction{{"search", "unknown"}}},
		{"missing action sanitized", `[{"type":"web_search_call","status":"completed"}]`, 2, 0, false, []SearchAction{{"unknown", "completed"}}},
		{"malformed action", `[{"type":"web_search_call","status":"completed","action":42}]`, 2, 0, false, []SearchAction{{"unknown", "completed"}}},
		{"unknown action below cap", `[{"type":"web_search_call","status":"completed","action":{"type":"private-action"}}]`, 2, 0, false, []SearchAction{{"unknown", "completed"}}},
		{"invalid cap cannot prove partial total", `[{"type":"web_search_call","status":"in_progress","action":{"type":"search"}}]`, 0, 0, false, []SearchAction{{"search", "in_progress"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, known, actions := InspectOpenAISearchUsage(json.RawMessage(tt.raw), tt.limit)
			if count != tt.count || known != tt.known || !reflect.DeepEqual(actions, tt.actions) {
				t.Fatalf("got (%d, %v, %#v), want (%d, %v, %#v)", count, known, actions, tt.count, tt.known, tt.actions)
			}
		})
	}
}

func TestInspectOpenAISearchUsageBoundsEvidence(t *testing.T) {
	raw := `[` + strings.Repeat(`{"type":"web_search_call","status":"completed","action":{"type":"search"}},`, 32) + `{"type":"web_search_call","status":"completed","action":{"type":"search"}}]`
	count, known, actions := InspectOpenAISearchUsage(json.RawMessage(raw), 32)
	if count != 33 || known || len(actions) != 32 {
		t.Fatalf("got count=%d known=%v evidence=%d; want retained count=33, unknown and bounded evidence=32", count, known, len(actions))
	}
}

func TestInspectOpenAISearchUsageCompletedBuiltinCap(t *testing.T) {
	const completedSearch = `{"type":"web_search_call","id":"search1","status":"completed","action":{"type":"search"}}`
	const pendingSearch = `{"type":"web_search_call","id":"search2","status":"searching","action":{"type":"search"}}`
	tests := []struct {
		name  string
		other string
		known bool
	}{
		{"completed page consumes final tool slot", `{"type":"web_search_call","id":"page","status":"completed","action":{"type":"open_page"}}`, true},
		{"completed find consumes final tool slot", `{"type":"web_search_call","id":"find","status":"completed","action":{"type":"find_in_page"}}`, true},
		{"unfinished page leaves search uncertain", `{"type":"web_search_call","id":"page","status":"in_progress","action":{"type":"open_page"}}`, false},
		{"failed find leaves search uncertain", `{"type":"web_search_call","id":"find","status":"failed","action":{"type":"find_in_page"}}`, false},
		{"duplicate cannot consume final slot", completedSearch, false},
		{"conflicting id invalidates cap proof", `{"type":"web_search_call","id":"search1","status":"completed","action":{"type":"open_page"}}`, false},
		{"malformed item invalidates cap proof", `{"type":"web_search_call","id":"page","status":"completed","action":{"type":"open_page"}},null`, false},
		{"malformed id invalidates cap proof", `{"type":"web_search_call","id":42,"status":"completed","action":{"type":"open_page"}}`, false},
		{"unknown action cannot consume final slot", `{"type":"web_search_call","id":"other","status":"completed","action":{"type":"other"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := json.RawMessage("[" + completedSearch + "," + tt.other + "," + pendingSearch + "]")
			count, known, _ := InspectOpenAISearchUsage(raw, 2)
			if count != 1 || known != tt.known {
				t.Fatalf("got search count=%d known=%v, want search count=1 known=%v", count, known, tt.known)
			}
		})
	}
}
