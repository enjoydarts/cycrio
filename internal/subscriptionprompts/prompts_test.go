package subscriptionprompts

import "testing"

func TestCriteriaCallersCannotChangeSharedDefaults(t *testing.T) {
	a := ClassificationCriteria()
	if len(a) != 3 {
		t.Fatal("expected exactly three gate labels")
	}
	for _, key := range []string{"subscription_related", "not_related", "uncertain"} {
		if a[key] == "" {
			t.Fatal("missing gate label", key)
		}
	}
	a["not_related"] = "mutated"
	delete(a, "uncertain")
	b := ClassificationCriteria()
	if b["not_related"] == "mutated" || b["uncertain"] == "" {
		t.Fatal("caller changed defaults for subsequent requests")
	}
}
