package notifications

import (
	"encoding/json"
	"testing"
)

func TestPushOnlyGenerationAndIndependentConsent(t *testing.T) {
	p := DefaultPreferences()
	p.Kinds[Renewal] = Channels{Push: true}
	if _, err := NormalizePreferences(p); err != ErrInvalid {
		t.Fatal("Push同意が必要", err)
	}
	p.PushOptIn = true
	rows, err := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{testContract()}}, p)
	if err != nil || len(rows) != 1 || !rows[0].Push || rows[0].Email || rows[0].InApp {
		t.Fatal(rows, err)
	}
	changed := rows[0]
	changed.Push, changed.Email, changed.InApp = false, true, true
	if !SameFacts(rows[0], changed) {
		t.Fatal("チャネル設定で出来事を分けない")
	}
	p.Kinds[Renewal] = Channels{Email: true}
	if _, err := NormalizePreferences(p); err != ErrInvalid {
		t.Fatal("Push同意をメールへ流用しない", err)
	}
}

func TestPushJSONRequiresExplicitBooleans(t *testing.T) {
	for _, raw := range []string{`{"in_app":true,"email":false}`, `{"in_app":true,"email":false,"push":null}`, `{"in_app":true,"email":false,"push":true,"push":false}`} {
		var c Channels
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatal(raw)
		}
	}
	p := DefaultPreferences()
	raw, _ := json.Marshal(p)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "push_opt_in")
	raw, _ = json.Marshal(fields)
	var next Preferences
	if json.Unmarshal(raw, &next) == nil {
		t.Fatal("Push同意の省略を保存しない")
	}
}
