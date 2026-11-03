package subscriptions

import (
	"encoding/json"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
)

const ChangeAlgorithmVersion = "subscription-change-v2"

var changeKeys = []string{"amount", "currency", "interval", "plan", "contract_kind", "effective_date", "access_end_date", "next_billing_date", "trial_end_date", "auto_renew"}
var changeToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

type ChangePreviewInput struct {
	ExpectedVersion   int64                `json:"expected_version"`
	SourceResultID    string               `json:"source_result_id"`
	SourceEventIndex  int                  `json:"source_event_index"`
	AdditionalSources []CandidateSourceRef `json:"additional_sources,omitempty"`
}
type ChangeInput struct {
	ChangePreviewInput
	RequestID      string   `json:"request_id"`
	PreviewToken   string   `json:"preview_token"`
	SelectedFields []string `json:"selected_fields"`
	Confirmed      bool     `json:"confirmed"`
}
type ChangeEvidence struct {
	ResultID      string     `json:"result_id"`
	EventIndex    int        `json:"event_index"`
	ReceivedAt    *time.Time `json:"received_at"`
	OriginalEvent ai.Event   `json:"original_event"`
}
type ChangeField struct {
	Key        string `json:"key"`
	Current    any    `json:"current"`
	Proposed   any    `json:"proposed"`
	Selectable bool   `json:"selectable"`
	Reason     string `json:"reason"`
}
type ChangeWarning struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
}
type ChangeInterval struct {
	Unit  string `json:"unit"`
	Count int    `json:"count"`
}
type ChangePreview struct {
	Subscription           Subscription     `json:"subscription"`
	Source                 ChangeEvidence   `json:"source"`
	AdditionalEvidence     []ChangeEvidence `json:"additional_evidence"`
	PreviewToken           string           `json:"preview_token"`
	AsOf                   string           `json:"as_of"`
	Timezone               string           `json:"timezone"`
	AlgorithmVersion       string           `json:"algorithm_version"`
	Fields                 []ChangeField    `json:"fields"`
	Warnings               []ChangeWarning  `json:"warnings"`
	DerivedNextBillingDate *string          `json:"derived_next_billing_date"`
}
type ChangeReceipt struct {
	RequestID          string           `json:"request_id"`
	AppliedVersion     int64            `json:"applied_version"`
	CreatedAt          time.Time        `json:"created_at"`
	SelectedFields     []string         `json:"selected_fields"`
	Before             Subscription     `json:"before"`
	After              Subscription     `json:"after"`
	Source             ChangeEvidence   `json:"source"`
	AdditionalEvidence []ChangeEvidence `json:"additional_evidence"`
	AlgorithmVersion   string           `json:"algorithm_version"`
}
type ChangeResult struct {
	Receipt ChangeReceipt `json:"receipt"`
	Current *Subscription `json:"current"`
}
type ChangePage struct {
	Changes []ChangeReceipt `json:"changes"`
	Total   int             `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

func ValidChangePreviewInput(in ChangePreviewInput) bool {
	return in.ExpectedVersion > 0 && ValidAdditionalSources(&in.SourceResultID, &in.SourceEventIndex, in.AdditionalSources)
}
func ValidChangeInput(in ChangeInput) bool {
	if !ValidChangePreviewInput(in.ChangePreviewInput) || !in.Confirmed || !ValidID(in.RequestID) || !changeToken.MatchString(in.PreviewToken) || len(in.SelectedFields) == 0 || len(in.SelectedFields) > len(changeKeys) {
		return false
	}
	seen := map[string]bool{}
	for _, k := range in.SelectedFields {
		if !slices.Contains(changeKeys, k) || seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}
func changeValue(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
func sameAmount(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return reflect.DeepEqual(a, b)
	}
	ar, ok := new(big.Rat).SetString(as)
	if !ok {
		return false
	}
	br, ok := new(big.Rat).SetString(bs)
	return ok && ar.Cmp(br) == 0
}

// BuildChangePreview never fills missing regular facts from transaction amounts.
// Additional sources are retained as evidence, never merged into the proposal.
func BuildChangePreview(s Subscription, source ChangeEvidence, additional []ChangeEvidence, today string) ChangePreview {
	p := ChangePreview{Subscription: s, Source: source, AdditionalEvidence: additional, AsOf: today, Timezone: "Asia/Tokyo", AlgorithmVersion: ChangeAlgorithmVersion, Fields: []ChangeField{}, Warnings: []ChangeWarning{}}
	if p.AdditionalEvidence == nil {
		p.AdditionalEvidence = []ChangeEvidence{}
	}
	e := source.OriginalEvent
	reason := ""
	if source.ReceivedAt == nil {
		reason = "unknown_source_date"
	}
	if e.EffectiveDate != nil && *e.EffectiveDate > today {
		reason = "future_effective"
	}
	if reason != "" {
		p.Warnings = append(p.Warnings, ChangeWarning{Code: reason})
	}
	add := func(key string, current, proposed any, blocked string) {
		if proposed == nil || reflect.DeepEqual(current, proposed) || key == "amount" && sameAmount(current, proposed) {
			return
		}
		r := reason
		if blocked != "" {
			r = blocked
		}
		p.Fields = append(p.Fields, ChangeField{Key: key, Current: current, Proposed: proposed, Selectable: r == "", Reason: r})
	}
	fixed := ""
	if s.ContractKind == "fixed_term" {
		fixed = "fixed_term_requires_clearing"
	}
	add("amount", changeValue(s.Amount), changeValue(e.RecurringAmount), fixed)
	add("currency", changeValue(s.Currency), changeValue(e.RecurringCurrency), fixed)
	if e.IntervalUnit != nil && e.IntervalCount != nil {
		var old any
		if s.IntervalUnit != nil && s.IntervalCount != nil {
			old = ChangeInterval{*s.IntervalUnit, *s.IntervalCount}
		}
		add("interval", old, ChangeInterval{*e.IntervalUnit, int(*e.IntervalCount)}, fixed)
	}
	add("plan", changeValue(s.Plan), changeValue(e.Plan), "")
	if e.ContractKind != nil && (*e.ContractKind == "recurring" || *e.ContractKind == "fixed_term") {
		blocked := ""
		if *e.ContractKind == "fixed_term" && (s.Amount != nil || s.Currency != nil || s.IntervalUnit != nil || s.NextBillingDate != nil) {
			blocked = "fixed_term_requires_clearing"
		}
		add("contract_kind", s.ContractKind, *e.ContractKind, blocked)
	}
	// Extraction's effective date can describe a price change; it is not proof
	// that the owner's original contract start date should be replaced.
	add("effective_date", changeValue(s.EffectiveDate), changeValue(e.EffectiveDate), "effective_date_requires_review")
	add("access_end_date", changeValue(s.AccessEndDate), changeValue(e.AccessEndDate), "")
	add("trial_end_date", changeValue(s.TrialEndDate), changeValue(e.TrialEndDate), "")
	if e.AutoRenew != nil {
		var prior any
		if s.AutoRenew != nil {
			prior = *s.AutoRenew
		}
		add("auto_renew", prior, *e.AutoRenew, "")
	}
	add("next_billing_date", changeValue(s.NextBillingDate), changeValue(e.NextBillingAt), fixed)
	if e.EventType == "cancellation" || e.EventType == "suspension" || e.EventType == "payment_failed" {
		p.Warnings = append(p.Warnings, ChangeWarning{Code: "cancellation_requires_review"})
	}
	if s.NextBillingMode == "automatic" && s.Status != "ended" && (s.AccessEndDate == nil || *s.AccessEndDate >= today) && changeFieldExists(p.Fields, "interval") {
		p.Warnings = append(p.Warnings, ChangeWarning{Code: "derived_billing_date", Field: "interval"})
	}
	return p
}
func changeFieldExists(fields []ChangeField, key string) bool {
	for _, f := range fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

func SelectChangeValues(v Values, p ChangePreview, selected []string) (Values, error) {
	seen := map[string]bool{}
	for _, key := range selected {
		if seen[key] {
			return Values{}, ErrInvalid
		}
		seen[key] = true
		var f *ChangeField
		for i := range p.Fields {
			if p.Fields[i].Key == key {
				f = &p.Fields[i]
				break
			}
		}
		if f == nil || !f.Selectable {
			return Values{}, ErrInvalid
		}
		raw, _ := json.Marshal(f.Proposed)
		var value string
		if key != "interval" && key != "auto_renew" {
			if json.Unmarshal(raw, &value) != nil {
				return Values{}, ErrInvalid
			}
		}
		switch key {
		case "amount":
			v.Amount = &value
		case "currency":
			v.Currency = &value
		case "plan":
			v.Plan = &value
		case "contract_kind":
			v.ContractKind = value
		case "access_end_date":
			v.AccessEndDate = &value
		case "trial_end_date":
			v.TrialEndDate = &value
		case "auto_renew":
			var renewal bool
			if json.Unmarshal(raw, &renewal) != nil {
				return Values{}, ErrInvalid
			}
			v.AutoRenew = &renewal
		case "next_billing_date":
			v.NextBillingDate = &value
			v.NextBillingMode = "manual"
		case "interval":
			var interval ChangeInterval
			if json.Unmarshal(raw, &interval) != nil {
				return Values{}, ErrInvalid
			}
			v.IntervalUnit = &interval.Unit
			v.IntervalCount = &interval.Count
		default:
			return Values{}, ErrInvalid
		}
	}
	if len(seen) == 0 {
		return Values{}, ErrInvalid
	}
	return v, nil
}
