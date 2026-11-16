package promptquality

import (
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/ai"
)

func TestCurrencyCorpusCoversExplicitContextualAndAmbiguousSymbols(t *testing.T) {
	cases := CurrencyCorpus()
	if len(cases) != 10 {
		t.Fatalf("need ten currency evidence cases, got %d", len(cases))
	}
	ids := map[string]bool{}
	ambiguous, mixed, explicit := 0, 0, 0
	for _, c := range cases {
		if ids[c.ID] || c.Text == "" || c.Label != "subscription_related" || len(c.Events) != 1 {
			t.Fatal("invalid or duplicate case", c.ID)
		}
		ids[c.ID] = true
		encoded, err := json.Marshal(ai.Extraction{Events: c.Events})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ai.ValidateExtraction(encoded); err != nil {
			t.Fatalf("invalid gold %s: %v", c.ID, err)
		}
		e := c.Events[0]
		if e.Amount == nil || e.RecurringAmount == nil {
			t.Fatal("both monetary scopes need known test amounts", c.ID)
		}
		if e.Currency == nil {
			ambiguous++
			if e.RecurringCurrency != nil {
				t.Fatal("ambiguous symbol leaked into recurring currency", c.ID)
			}
		} else {
			explicit++
			if e.RecurringCurrency != nil && *e.Currency != *e.RecurringCurrency {
				mixed++
			}
		}
	}
	if ambiguous != 2 || mixed != 1 || explicit != 8 {
		t.Fatal("missing ambiguity or independent currency coverage", ambiguous, mixed, explicit)
	}
}
