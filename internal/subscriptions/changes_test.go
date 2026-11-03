package subscriptions

import (
	"github.com/enjoydarts/cycrio/internal/ai"
	"testing"
	"time"
)

func changeString(s string) *string { return &s }
func changeField(p ChangePreview, key string) *ChangeField {
	for i := range p.Fields {
		if p.Fields[i].Key == key {
			return &p.Fields[i]
		}
	}
	return nil
}

func TestChangeProposalsUseOnlyExplicitRecurringFacts(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	s := Subscription{Version: 1, Values: Values{Service: "Example", Status: "active", ContractKind: "recurring", Amount: changeString("10"), Currency: changeString("USD")}}
	src := ChangeEvidence{ReceivedAt: &now, OriginalEvent: ai.Event{EventType: "refund", Amount: changeString("100"), Currency: changeString("JPY"), RecurringAmount: changeString("0")}}
	p := BuildChangePreview(s, src, nil, "2026-11-03")
	if f := changeField(p, "amount"); f == nil || f.Proposed != "0" || !f.Selectable {
		t.Fatal(p)
	}
	if changeField(p, "currency") != nil || changeField(p, "status") != nil {
		t.Fatal("transaction facts must not fill regular charge", p)
	}
	src.OriginalEvent.RecurringAmount = changeString("10.00")
	if changeField(BuildChangePreview(s, src, nil, "2026-11-03"), "amount") != nil {
		t.Fatal("decimal-equivalent amount is unchanged")
	}
}

func TestChangeProposalSafetyAndSelectedFields(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	s := Subscription{Version: 1, Values: Values{Service: "Example", Status: "active", ContractKind: "recurring", Amount: changeString("10"), Currency: changeString("USD"), NextBillingDate: changeString("2026-11-30"), NextBillingMode: "automatic"}}
	src := ChangeEvidence{ReceivedAt: &now, OriginalEvent: ai.Event{EventType: "cancellation", RecurringAmount: changeString("20"), RecurringCurrency: changeString("JPY"), NextBillingAt: changeString("2026-12-01")}}
	p := BuildChangePreview(s, src, nil, "2026-11-03")
	v, e := SelectChangeValues(s.Values, p, []string{"amount"})
	if e != nil || *v.Amount != "20" || *v.Currency != "USD" || *v.NextBillingDate != "2026-11-30" || v.Status != "active" || v.NextBillingMode != "automatic" {
		t.Fatal(v, e)
	}
	v, e = SelectChangeValues(s.Values, p, []string{"next_billing_date"})
	if e != nil || v.NextBillingMode != "manual" || *v.NextBillingDate != "2026-12-01" {
		t.Fatal(v, e)
	}
	src.OriginalEvent.EffectiveDate = changeString("2026-12-01")
	p = BuildChangePreview(s, src, nil, "2026-11-03")
	if f := changeField(p, "amount"); f == nil || f.Selectable || f.Reason != "future_effective" {
		t.Fatal(p)
	}
	if _, e = SelectChangeValues(s.Values, p, []string{"amount"}); e != ErrInvalid {
		t.Fatal(e)
	}
	src.OriginalEvent.EffectiveDate = nil
	src.ReceivedAt = nil
	if changeField(BuildChangePreview(s, src, nil, "2026-11-03"), "amount").Selectable {
		t.Fatal("unknown chronology must require review")
	}
}

func TestChangeInputRejectsUnselectedUnknownAndDuplicateFields(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	in := ChangeInput{ChangePreviewInput: ChangePreviewInput{ExpectedVersion: 1, SourceResultID: id}, RequestID: id, PreviewToken: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Confirmed: true, SelectedFields: []string{"amount"}}
	if !ValidChangeInput(in) {
		t.Fatal(in)
	}
	for _, fields := range [][]string{nil, {"amount", "amount"}, {"status"}, {"display_name"}} {
		in.SelectedFields = fields
		if ValidChangeInput(in) {
			t.Fatal(fields)
		}
	}
}

func TestChangeUnknownContractKindIsNotEvidence(t *testing.T) {
	now := time.Now()
	p := BuildChangePreview(Subscription{Values: Values{ContractKind: "recurring"}}, ChangeEvidence{ReceivedAt: &now, OriginalEvent: ai.Event{ContractKind: changeString("unknown")}}, nil, "2026-11-03")
	if changeField(p, "contract_kind") != nil {
		t.Fatal(p)
	}
}
