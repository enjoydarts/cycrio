package autoregistration

import (
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func identity(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
func label(p *string) string {
	if p == nil {
		return ""
	}
	return identity(*p)
}

// Match ignores amounts, and never merges differing known plans or currencies.
// A missing plan is ambiguous if either side has a known plan.
type Candidate struct {
	subscriptions.Subscription
	CurrencyAmbiguous bool
}

func Match(event ai.Event, candidates []Candidate) (string, bool) {
	id := ""
	count := 0
	ambiguous := false
	for _, s := range candidates {
		if identity(s.Service) != identity(event.Service) {
			continue
		}
		if s.CurrencyAmbiguous {
			ambiguous = true
			continue
		}
		if s.Currency != nil && event.Currency != nil && *s.Currency != *event.Currency {
			continue
		}
		plan, wanted := label(s.Plan), label(event.Plan)
		if plan != wanted {
			if plan == "" || wanted == "" {
				ambiguous = true
			}
			continue
		}
		id = s.ID
		count++
	}
	if ambiguous || count > 1 {
		return "", true
	}
	return id, false
}

// Values keeps transaction totals in the immutable source, never as recurring
// contract prices. Unknown pairs and status remain explicitly unknown.
func Values(event ai.Event) (subscriptions.Values, error) {
	v := subscriptions.Values{Service: event.Service, Plan: event.Plan, Status: "active", NextBillingDate: event.NextBillingAt}
	if event.IntervalUnit != nil && event.IntervalCount != nil {
		if *event.IntervalCount < 1 || *event.IntervalCount > 10000 {
			return subscriptions.Values{}, subscriptions.ErrInvalid
		}
		n := int(*event.IntervalCount)
		v.IntervalUnit = event.IntervalUnit
		v.IntervalCount = &n
	}
	// Validate obtained amounts even though they are not a contract rate.
	if event.Amount != nil && event.Currency != nil {
		v.Amount = event.Amount
		v.Currency = event.Currency
	}
	checked, err := subscriptions.Normalize(v)
	if err != nil {
		return subscriptions.Values{}, err
	}
	checked.Amount = nil
	checked.Currency = nil
	checked.Status = "unknown"
	return checked, nil
}

// EventDate uses an explicit observation date, never a future billing date or
// the time at which a historical message happened to be processed.
func EventDate(event ai.Event) (*string, *string) {
	for _, source := range []struct {
		date  *string
		basis string
	}{{event.BilledAt, "billed_at"}, {event.PeriodStart, "period_start"}, {event.PeriodEnd, "period_end"}} {
		if source.date == nil {
			continue
		}
		parsed, err := time.Parse("2006-01-02", *source.date)
		if err != nil || parsed.Year() < 1 || parsed.Format("2006-01-02") != *source.date {
			return nil, nil
		}
		basis := source.basis
		return source.date, &basis
	}
	return nil, nil
}
