package promptquality

import (
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/ai"
	"strings"
	"testing"
)

func TestCorpusFrozenComposition(t *testing.T) {
	cases := Corpus()
	dev, hold := 0, 0
	ids := map[string]bool{}
	for _, c := range cases {
		if ids[c.ID] || c.Text == "" {
			t.Fatal("invalid case")
		}
		ids[c.ID] = true
		if c.Split == "development" {
			dev++
		} else if c.Split == "holdout" {
			hold++
		}
	}
	if dev != 32 || hold != 12 {
		t.Fatalf("composition %d/%d", dev, hold)
	}
}
func TestScoreRecurringMoneyAndUnnamedEvent(t *testing.T) {
	a, b, currency := "1500.00", "1500", "JPY"
	gold := ai.Event{EventType: "payment", RecurringAmount: &a, RecurringCurrency: &currency}
	got := gold
	got.RecurringAmount = &b
	score := ScoreEvents([]ai.Event{gold}, []ai.Event{got})
	if score.TP != 1 || score.KnownCorrect != score.KnownTotal || score.Hallucinated != 0 {
		t.Fatalf("explicit recurring price decimal equivalence: %+v", score)
	}
	got.RecurringAmount = nil
	if s := ScoreEvents([]ai.Event{gold}, []ai.Event{got}); s.KnownCorrect >= s.KnownTotal {
		t.Fatal("missing recurring price not penalized")
	}
	got = gold
	got.Service = "Invented brand"
	if s := ScoreEvents([]ai.Event{gold}, []ai.Event{got}); s.FN != 1 || s.FP != 1 {
		t.Fatal("invented service accepted", s)
	}
}
func TestCorpusExplicitRecurringKindIsNotScoredAsInvented(t *testing.T) {
	want := map[string]bool{"d01": true, "d02": true, "d04": true, "d05": true, "d06": true, "d08": true, "d10": true, "d12": true, "h01": true, "h02": true, "r04": true, "r06": true, "r07": true, "r12": true}
	for _, c := range Corpus() {
		if !want[c.ID] {
			continue
		}
		event := c.Events[0]
		event.ContractKind = str("recurring")
		if s := ScoreEvents(c.Events, []ai.Event{event}); s.Hallucinated != 0 {
			t.Fatal(c.ID, "explicit recurring contract penalized as hallucination", s)
		}
	}
}
func TestScoreFactsDecimalMissingAndHallucination(t *testing.T) {
	amount, currency := "12.00", "USD"
	gold := []ai.Event{{Service: "Fable", EventType: "payment", Amount: &amount, Currency: &currency}}
	got := gold[0]
	equivalent := "12"
	got.Amount = &equivalent
	s := ScoreEvents(gold, []ai.Event{got})
	if s.TP != 1 || s.KnownCorrect != s.KnownTotal || s.Hallucinated != 0 {
		t.Fatalf("equivalent: %+v", s)
	}
	got.Amount = nil
	got.Plan = &currency
	s = ScoreEvents(gold, []ai.Event{got})
	if s.KnownCorrect >= s.KnownTotal || s.Hallucinated != 1 {
		t.Fatalf("missing/invented: %+v", s)
	}
	s = ScoreEvents(gold, nil)
	if s.FN != 1 || s.KnownCorrect != 0 || s.KnownTotal == 0 {
		t.Fatalf("empty %+v", s)
	}
	s = ScoreEvents(nil, []ai.Event{got})
	if s.FP != 1 {
		t.Fatal(s)
	}
}
func TestScoreOrderAndServiceType(t *testing.T) {
	a := ai.Event{Service: "Fable", EventType: "trial"}
	b := ai.Event{Service: "Moss", EventType: "cancellation"}
	s := ScoreEvents([]ai.Event{a, b}, []ai.Event{b, a})
	if s.TP != 2 || s.FP+s.FN != 0 {
		t.Fatal(s)
	}
	b.EventType = "renewal"
	s = ScoreEvents([]ai.Event{a}, []ai.Event{b})
	if s.FP != 1 || s.FN != 1 {
		t.Fatal(s)
	}
}
func TestReportNeverSerializesBodies(t *testing.T) {
	b, e := json.Marshal(NewReport())
	if e != nil || strings.Contains(string(b), Corpus()[0].Text) {
		t.Fatal("body leakage")
	}
}
func TestCorpusGoldMatchesOutputSchemaAndCallBudget(t *testing.T) {
	if len(Corpus())*len(Variants())*2 != MaxPaidCalls {
		t.Fatal("corpus exceeds explicitly announced paid budget")
	}
	for _, c := range Corpus() {
		events := c.Events
		if events == nil {
			events = []ai.Event{}
		}
		b, err := json.Marshal(ai.Extraction{Events: events})
		if err != nil {
			t.Fatal(c.ID, err)
		}
		if _, err = ai.ValidateExtraction(b); err != nil {
			t.Fatal(c.ID, "invalid ground truth", err)
		}
	}
}
func TestSummaryUnknownCostsAndTechnicalErrorsExcluded(t *testing.T) {
	r := NewReport()
	name := Variants()[0].Name
	amount, currency := "0.0000000000000000000000000000000000000001", "USD"
	r.Results = []Result{{CaseID: "d01", Variant: name, Stage: "classification", Outcome: "provider_unavailable", Cost: ai.Cost{Status: "unknown"}}, {CaseID: "d02", Variant: name, Stage: "classification", Outcome: "success", Label: "subscription_related", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &currency}}}
	r.summarize()
	m := r.Metrics[name+"/all"]
	if m.ClassificationCovered != 1 || m.TechnicalErrors != 1 || m.FN != 0 {
		t.Fatal(m)
	}
	if r.UnknownCostCalls != 1 || r.KnownCosts["USD"] != amount {
		t.Fatalf("costs %+v", r)
	}
}
