package notifications

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNotificationChannelJSONRequiresExplicitKnownBooleans(t *testing.T) {
	for _, raw := range []string{`{"in_app":true}`, `{"in_app":null,"email":false}`, `{"in_app":true,"email":false,"unknown":true}`, `{"in_app":true,"in_app":false,"email":false}`} {
		var channels Channels
		if json.Unmarshal([]byte(raw), &channels) == nil {
			t.Fatal(raw)
		}
	}
}

const testContractID = "11111111-1111-4111-8111-111111111111"
const testResultID = "22222222-2222-4222-8222-222222222222"

func ptr[T any](v T) *T          { return &v }
func notificationNow() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) }
func testContract() Contract {
	return Contract{ID: testContractID, Service: "Example", Status: "active", ContractKind: "recurring", NextBillingDate: ptr("2026-11-10"), NextDateSource: "manual", IntervalUnit: ptr("month"), IntervalCount: ptr(1)}
}

func TestPreferencesRequireExplicitEmailOptInAndKnownKinds(t *testing.T) {
	p := DefaultPreferences()
	if p.LeadDays != 7 || p.SendHour != 9 || p.Timezone != "Asia/Tokyo" || p.EmailOptIn {
		t.Fatal(p)
	}
	for _, v := range p.Kinds {
		if !v.InApp || v.Email {
			t.Fatal(v)
		}
	}
	if _, e := NormalizePreferences(p); e != nil {
		t.Fatal(e)
	}
	p.Kinds[Renewal] = Channels{InApp: true, Email: true}
	if _, e := NormalizePreferences(p); e != ErrInvalid {
		t.Fatal("email needs explicit opt in", e)
	}
	p.EmailOptIn = true
	if _, e := NormalizePreferences(p); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []func(*Preferences){func(p *Preferences) { p.LeadDays = 31 }, func(p *Preferences) { p.LeadDays = -1 }, func(p *Preferences) { p.SendHour = 24 }, func(p *Preferences) { p.Timezone = "UTC" }, func(p *Preferences) { p.Kinds[Kind("unknown")] = Channels{} }, func(p *Preferences) { delete(p.Kinds, TrialEnd) }} {
		next := DefaultPreferences()
		bad(&next)
		if _, e := NormalizePreferences(next); e != ErrInvalid {
			t.Fatal(next, e)
		}
	}
}

func TestGenerateStableOccurrenceKeysAndJapanBoundary(t *testing.T) {
	p := DefaultPreferences()
	c := testContract()
	in := GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}
	first, e := Generate(in, p)
	if e != nil || len(first) != 1 || first[0].Kind != Renewal || first[0].Email || !first[0].InApp {
		t.Fatal(first, e)
	}
	if first[0].ScheduledAt.Before(in.Now) {
		t.Fatal("late eligible reminder should be scheduled now", first)
	}
	in.Now = in.Now.Add(24 * time.Hour)
	p.Version++
	again, e := Generate(in, p)
	if e != nil || len(again) != 1 || again[0].SourceKey != first[0].SourceKey {
		t.Fatal(first, again, e)
	}
	c.NextBillingDate = ptr("2026-11-03")
	p.LeadDays = 0
	in = GenerationInput{Now: time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC), Contracts: []Contract{c}}
	rows, e := Generate(in, p)
	if e != nil || len(rows) != 1 || rows[0].ScheduledAt.Format(time.RFC3339) != "2026-11-03T09:00:00+09:00" {
		t.Fatal(rows, e)
	}
	in.Now = in.Now.Add(-time.Second)
	rows, e = Generate(in, p)
	if e != nil || len(rows) != 0 {
		t.Fatal("previous Japan day", rows, e)
	}
	c.NextBillingDate = ptr("2026-02-30")
	in.Contracts = []Contract{c}
	if _, e = Generate(in, p); e != ErrInvalid {
		t.Fatal(e)
	}
}

func TestGenerateLifecycleSuppressionAndExplicitTrial(t *testing.T) {
	for _, mutate := range []func(*Contract){func(c *Contract) { c.Status = "ended" }, func(c *Contract) { c.Deleted = true }, func(c *Contract) { c.ContractKind = "fixed_term" }, func(c *Contract) { c.AccessEndDate = ptr("2026-11-02") }, func(c *Contract) { c.AccessEndDate = ptr("2026-11-09") }, func(c *Contract) { c.AutoRenew = ptr(false) }} {
		c := testContract()
		mutate(&c)
		rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
		if e != nil || len(rows) != 0 {
			t.Fatal(c, rows, e)
		}
	}
	c := testContract()
	c.NextBillingDate = nil
	c.TrialEndDate = ptr("2026-11-03")
	c.AccessEndDate = ptr("2026-11-03")
	c.AutoRenew = ptr(false)
	rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 1 || rows[0].Kind != TrialEnd {
		t.Fatal(rows, e)
	}
	c.ContractKind = "fixed_term"
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 1 || rows[0].Kind != TrialEnd {
		t.Fatal("an explicit trial ending is relevant before finite membership", rows, e)
	}
	c.TrialEndDate = nil
	c.Amount = ptr("0")
	c.Currency = ptr("JPY")
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 0 {
		t.Fatal("zero price is not a trial", rows, e)
	}
	c = testContract()
	c.IntervalUnit = ptr("year")
	c.AutoRenew = ptr(false)
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 1 || rows[0].Kind != AnnualCharge {
		t.Fatal(rows, e)
	}
	c.NextDateSource = "receipt_estimate"
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}

func TestNativePriceComparisonAndPendingEvidence(t *testing.T) {
	for _, tc := range []struct {
		old, new, oldCurrency, newCurrency string
		want                               bool
	}{{"0", "0.01", "JPY", "JPY", true}, {"10.00", "10", "USD", "USD", false}, {"999999999999999999999999", "999999999999999999999999.000000000001", "USD", "USD", true}, {"10", "20", "USD", "JPY", false}, {"20", "10", "USD", "USD", false}} {
		got, e := NativePriceIncrease(ptr(tc.old), ptr(tc.oldCurrency), ptr(tc.new), ptr(tc.newCurrency))
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	if got, e := NativePriceIncrease(nil, ptr("USD"), ptr("20"), ptr("USD")); e != nil || got {
		t.Fatal(got, e)
	}
	if _, e := NativePriceIncrease(ptr("NaN"), ptr("USD"), ptr("20"), ptr("USD")); e != ErrInvalid {
		t.Fatal(e)
	}
	c := testContract()
	c.NextBillingDate = nil
	c.Amount, c.Currency = ptr("10"), ptr("USD")
	price := PendingPriceChange{ContractID: c.ID, ResultID: testResultID, EventIndex: 0, Pending: true, OldAmount: ptr("10"), OldCurrency: ptr("USD"), NewAmount: ptr("20"), NewCurrency: ptr("USD")}
	rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}, PriceChanges: []PendingPriceChange{price}}, DefaultPreferences())
	if e != nil || len(rows) != 1 || rows[0].Kind != PriceIncrease {
		t.Fatal(rows, e)
	}
	price.Pending = false
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}, PriceChanges: []PendingPriceChange{price}}, DefaultPreferences())
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}

func TestPriceCandidateRequiresCurrentContractBaseline(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		currentAmount, currentCurrency *string
		oldAmount                      string
		wantError                      bool
	}{
		{"current price differs", ptr("30"), ptr("USD"), "10", true},
		{"current price unknown", nil, nil, "10", true},
		{"current currency differs", ptr("10"), ptr("JPY"), "10", true},
		{"numeric equivalents", ptr("10.00"), ptr("USD"), "10.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testContract()
			c.NextBillingDate = nil
			c.Amount, c.Currency = tc.currentAmount, tc.currentCurrency
			price := PendingPriceChange{ContractID: c.ID, ResultID: testResultID, Pending: true, OldAmount: ptr(tc.oldAmount), OldCurrency: ptr("USD"), NewAmount: ptr("20"), NewCurrency: ptr("USD")}
			rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}, PriceChanges: []PendingPriceChange{price}}, DefaultPreferences())
			if tc.wantError {
				if e != ErrInvalid {
					t.Fatal(rows, e)
				}
				return
			}
			if e != nil || len(rows) != 1 || rows[0].OldAmount == nil || *rows[0].OldAmount != *c.Amount {
				t.Fatal(rows, e)
			}
		})
	}
}

func TestImportEpisodesAndSafeFixedEmailTemplates(t *testing.T) {
	episode := ImportEpisode{AccountID: testContractID, EpisodeID: testResultID, Active: true, Reason: "authentication_failed"}
	rows, e := Generate(GenerationInput{Now: notificationNow(), ImportEpisodes: []ImportEpisode{episode, episode}}, DefaultPreferences())
	if e != nil || len(rows) != 1 || rows[0].Kind != ImportError {
		t.Fatal(rows, e)
	}
	episode.ManualStop = true
	if rows, e = Generate(GenerationInput{Now: notificationNow(), ImportEpisodes: []ImportEpisode{episode}}, DefaultPreferences()); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	c := testContract()
	c.Service = `<img src=x onerror=alert(1)> & Example`
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if _, e = RenderEmail(rows[0]); e != ErrInvalid {
		t.Fatal("disabled email candidates must not render", e)
	}
	p := DefaultPreferences()
	p.EmailOptIn = true
	p.Kinds[Renewal] = Channels{InApp: true, Email: true}
	rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	content, e := RenderEmail(rows[0])
	if e != nil || strings.Contains(content.HTML, "<img") || !strings.Contains(content.HTML, "&lt;img") || strings.ContainsAny(content.Subject, "\r\n") {
		t.Fatal(content, e)
	}
	if strings.Contains(content.HTML, "<a ") || strings.Contains(content.HTML, "<img ") {
		t.Fatal("templates must not create external resource links", content.HTML)
	}
	rows[0].Kind = Kind("unknown")
	if _, e = RenderEmail(rows[0]); e != ErrInvalid {
		t.Fatal(e)
	}
}

func TestGenerateRejectsMalformedRecordsAndNormalizesIdentities(t *testing.T) {
	for _, mutate := range []func(*Contract){func(c *Contract) { c.ID = "bad" }, func(c *Contract) { c.Service = "bad\nservice" }, func(c *Contract) { c.Amount = ptr("-1") }, func(c *Contract) { c.Currency = ptr("usd") }, func(c *Contract) { c.TrialEndDate = ptr("2026-2-03") }, func(c *Contract) { c.AccessEndDate = ptr("0000-01-01") }, func(c *Contract) { c.NextDateSource = "inferred" }, func(c *Contract) { c.IntervalCount = ptr(0) }} {
		c := testContract()
		mutate(&c)
		if _, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences()); e != ErrInvalid {
			t.Fatal(c, e)
		}
	}
	zero := "00000000-0000-0000-0000-000000000000"
	c0 := testContract()
	c0.ID = zero
	if _, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c0}}, DefaultPreferences()); e != ErrInvalid {
		t.Fatal("nil contract identity", e)
	}
	if _, e := Generate(GenerationInput{Now: notificationNow(), ImportEpisodes: []ImportEpisode{{AccountID: testContractID, EpisodeID: zero, Active: true, Reason: "authentication_failed"}}}, DefaultPreferences()); e != ErrInvalid {
		t.Fatal("nil source episode identity", e)
	}
	c := testContract()
	c.ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	first, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil {
		t.Fatal(e)
	}
	c.ID = strings.ToUpper(c.ID)
	second, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, DefaultPreferences())
	if e != nil || first[0].SourceKey != second[0].SourceKey || *second[0].ContractID != strings.ToLower(c.ID) {
		t.Fatal(first, second, e)
	}
	p := DefaultPreferences()
	p.Kinds[Renewal] = Channels{}
	if rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	badPrice := PendingPriceChange{ContractID: c.ID, ResultID: "bad", Pending: true, OldAmount: ptr("1"), OldCurrency: ptr("USD"), NewAmount: ptr("2"), NewCurrency: ptr("USD")}
	if _, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}, PriceChanges: []PendingPriceChange{badPrice}}, DefaultPreferences()); e != ErrInvalid {
		t.Fatal(e)
	}
	badPrice.ResultID = testResultID
	badPrice.EventIndex = 32
	if _, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}, PriceChanges: []PendingPriceChange{badPrice}}, DefaultPreferences()); e != ErrInvalid {
		t.Fatal(e)
	}
	if _, e := Generate(GenerationInput{Now: notificationNow(), ImportEpisodes: []ImportEpisode{{AccountID: testContractID, EpisodeID: testResultID, Active: true, Reason: "bad\nreason"}}}, DefaultPreferences()); e != ErrInvalid {
		t.Fatal(e)
	}
}
