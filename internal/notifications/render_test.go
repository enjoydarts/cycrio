package notifications

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAllNotificationKindsRenderFixedTemplatesAndStableIdentities(t *testing.T) {
	p := DefaultPreferences()
	p.EmailOptIn = true
	for kind := range p.Kinds {
		p.Kinds[kind] = Channels{InApp: true, Email: true}
	}
	annual := testContract()
	annual.Amount, annual.Currency = ptr("10"), ptr("USD")
	annual.IntervalUnit = ptr("year")
	annual.TrialEndDate = ptr("2026-11-04")
	monthly := testContract()
	monthly.ID = "33333333-3333-4333-8333-333333333333"
	price := PendingPriceChange{ContractID: annual.ID, ResultID: testResultID, Pending: true, OldAmount: ptr("10"), OldCurrency: ptr("USD"), NewAmount: ptr("20"), NewCurrency: ptr("USD")}
	episode := ImportEpisode{AccountID: testContractID, EpisodeID: testResultID, Active: true, Reason: "authentication_failed"}
	in := GenerationInput{Now: notificationNow(), Contracts: []Contract{annual, monthly}, PriceChanges: []PendingPriceChange{price}, ImportEpisodes: []ImportEpisode{episode}}
	rows, e := Generate(in, p)
	if e != nil || len(rows) != 5 {
		t.Fatal(rows, e)
	}
	seen := map[Kind]bool{}
	keys := []string{}
	for _, row := range rows {
		content, e := RenderEmail(row)
		if e != nil || !strings.HasPrefix(content.Subject, "Cycrio: ") || content.Text == "" || content.HTML == "" {
			t.Fatal(row, content, e)
		}
		if row.Kind == PriceIncrease && (!strings.Contains(content.Text, "10 USD") || !strings.Contains(content.Text, "20 USD")) {
			t.Fatal(content)
		}
		if row.Kind == TrialEnd && strings.Contains(content.Text, "請求日") {
			t.Fatal("trial end cannot invent a charge", content)
		}
		seen[row.Kind] = true
		keys = append(keys, row.SourceKey)
	}
	for _, kind := range kinds {
		if !seen[kind] {
			t.Fatal(kind)
		}
	}
	in.Contracts = []Contract{monthly, annual}
	in.Now = in.Now.Add(24 * time.Hour)
	p.Version++
	next, e := Generate(in, p)
	if e != nil {
		t.Fatal(e)
	}
	nextKeys := []string{}
	for _, row := range next {
		nextKeys = append(nextKeys, row.SourceKey)
	}
	if !reflect.DeepEqual(keys, nextKeys) {
		t.Fatal(keys, nextKeys)
	}
	bad := rows[0]
	bad.SourceKey = "untrusted"
	if _, e = RenderEmail(bad); e != ErrInvalid {
		t.Fatal(e)
	}
	bad = rows[0]
	bad.Currency = ptr(`<script>`)
	if _, e = RenderEmail(bad); e != ErrInvalid {
		t.Fatal(e)
	}
}

func TestDateWindowInclusiveBoundsAndUnknownRegularFacts(t *testing.T) {
	p := DefaultPreferences()
	p.LeadDays = 30
	c := testContract()
	c.NextBillingDate = ptr("2026-12-03")
	c.Amount = ptr("0")
	c.Currency = nil
	rows, e := Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p)
	if e != nil || len(rows) != 1 || rows[0].Amount == nil || rows[0].Currency != nil {
		t.Fatal(rows, e)
	}
	c.NextBillingDate = ptr("2026-12-04")
	if rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	c.NextBillingDate = ptr("2026-11-02")
	if rows, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p); e != nil || len(rows) != 0 {
		t.Fatal("past occurrences are not new reminders", rows, e)
	}
	c.NextBillingDate = ptr("2028-02-29")
	if _, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p); e != nil {
		t.Fatal(e)
	}
	c.NextBillingDate = ptr("2026-02-29")
	if _, e = Generate(GenerationInput{Now: notificationNow(), Contracts: []Contract{c}}, p); e != ErrInvalid {
		t.Fatal(e)
	}
}
