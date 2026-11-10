package notifications

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPendingReviewAggregateAndValidation(t *testing.T) {
	p := DefaultPreferences()
	p.EmailOptIn, p.PushOptIn = true, true
	p.Kinds[PendingReview] = Channels{InApp: true, Email: true, Push: true}
	review := PendingReviewRun{RunID: testResultID, Count: 17, Pending: true, Channels: Channels{InApp: true, Email: true, Push: true}}
	rows, err := Generate(GenerationInput{Now: notificationNow(), ReviewRuns: []PendingReviewRun{review, review}}, p)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	c := rows[0]
	if c.ReviewRunID == nil || *c.ReviewRunID != testResultID || c.ReviewCount == nil || *c.ReviewCount != 17 || !c.ScheduledAt.Equal(notificationNow()) {
		t.Fatal(c)
	}
	content, err := RenderEmail(c)
	if err != nil || !strings.Contains(content.Text, "通知作成時点で17件") || !strings.Contains(content.Text, "Cycrio") {
		t.Fatal(content, err)
	}
	for _, bad := range []PendingReviewRun{{RunID: "invalid", Count: 1, Pending: true}, {RunID: testResultID, Count: 0, Pending: true}} {
		if _, err := Generate(GenerationInput{Now: notificationNow(), ReviewRuns: []PendingReviewRun{bad}}, p); err != ErrInvalid {
			t.Fatal(bad, err)
		}
	}
	review.Pending = false
	if rows, err := Generate(GenerationInput{Now: notificationNow(), ReviewRuns: []PendingReviewRun{review}}, p); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestPendingReviewSettingsRequireSixKinds(t *testing.T) {
	p := DefaultPreferences()
	if len(p.Kinds) != 6 || p.Kinds[PendingReview] != (Channels{InApp: true}) {
		t.Fatal(p)
	}
	delete(p.Kinds, PendingReview)
	raw, _ := json.Marshal(p)
	var input Preferences
	if json.Unmarshal(raw, &input) == nil {
		t.Fatal("更新APIは旧形式を拒否する")
	}
	if err := UnmarshalStoredPreferences(raw, &input); err != nil || input.Kinds[PendingReview] != (Channels{InApp: true}) {
		t.Fatal("保存済み旧設定は初期値を補完する", input, err)
	}
	input.Kinds[Renewal] = Channels{Email: true}
	input.EmailOptIn = true
	raw, _ = json.Marshal(input)
	var roundtrip Preferences
	if err := UnmarshalStoredPreferences(raw, &roundtrip); err != nil || !roundtrip.Kinds[Renewal].Email || roundtrip.Kinds[PendingReview].Email || roundtrip.Kinds[PendingReview].Push {
		t.Fatal(roundtrip, err)
	}
}

func TestPendingReviewStoredPreferencesRejectsNullKinds(t *testing.T) {
	raw, _ := json.Marshal(DefaultPreferences())
	var doc map[string]json.RawMessage
	_ = json.Unmarshal(raw, &doc)
	doc["kinds"] = json.RawMessage("null")
	raw, _ = json.Marshal(doc)
	var p Preferences
	if err := UnmarshalStoredPreferences(raw, &p); err != ErrInvalid {
		t.Fatal(err)
	}
}
