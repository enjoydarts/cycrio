package subscriptions

import (
	"github.com/enjoydarts/cycrio/internal/ai"
	"testing"
	"time"
)

func TestScheduledPreviewKeepsTodayAndExistingGuards(t *testing.T) {
	now := time.Now()
	date := "2027-01-02"
	s := Subscription{Values: Values{Service: "Synthetic", Amount: changeString("10"), Currency: changeString("JPY"), ContractKind: "recurring", Status: "active"}}
	source := ChangeEvidence{ReceivedAt: &now, OriginalEvent: ai.Event{RecurringAmount: changeString("20"), EffectiveDate: &date}}
	p := BuildScheduledChangePreview(s, source, nil, "2027-01-01")
	if p.AsOf != "2027-01-01" || p.ScheduledFor != date || !changeField(p.ChangePreview, "amount").Selectable || changeField(p.ChangePreview, "effective_date").Selectable {
		t.Fatal(p)
	}
	if changeField(BuildChangePreview(s, source, nil, "2027-01-01"), "amount").Selectable {
		t.Fatal("immediate preview must keep future guard")
	}
	source.ReceivedAt = nil
	p = BuildScheduledChangePreview(s, source, nil, "2027-01-01")
	if f := changeField(p.ChangePreview, "amount"); f.Selectable || f.Reason != "unknown_source_date" {
		t.Fatal(p)
	}
}
