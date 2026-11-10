package notifications

import (
	"reflect"
	"slices"
	"strconv"
	"time"
)

func reference[T any](value T) *T { return &value }
func copyText(value *string) *string {
	if value == nil {
		return nil
	}
	return reference(*value)
}
func reminderEligible(c Contract, today string) bool {
	return !c.Deleted && c.Status != "ended" && (c.AccessEndDate == nil || *c.AccessEndDate >= today)
}
func annual(c Contract) bool {
	return c.IntervalUnit != nil && c.IntervalCount != nil && (*c.IntervalUnit == "year" && *c.IntervalCount == 1 || *c.IntervalUnit == "month" && *c.IntervalCount == 12)
}
func occurrenceKey(kind Kind, id, date string) string { return string(kind) + ":" + id + ":" + date }
func priceKey(id, result string, index int) string {
	return string(PriceIncrease) + ":" + id + ":" + result + ":" + strconv.Itoa(index)
}
func episodeKey(account, episode string) string {
	return string(ImportError) + ":" + account + ":" + episode
}

// Generate creates stable identities, not database IDs or delivery attempts.
// Repositories must enforce owner/source-key uniqueness across days and settings
// changes, and recheck current eligibility before enqueueing email delivery.
func Generate(in GenerationInput, p Preferences) ([]Candidate, error) {
	p, e := NormalizePreferences(p)
	if e != nil {
		return nil, e
	}
	now := in.Now.In(japan)
	if in.Now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
		return nil, ErrInvalid
	}
	today := now.Format("2006-01-02")
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, japan)
	end := midnight.AddDate(0, 0, p.LeadDays)
	contracts := map[string]Contract{}
	for _, raw := range in.Contracts {
		c, e := normalizeContract(raw)
		if e != nil {
			return nil, e
		}
		if prior, ok := contracts[c.ID]; ok && !reflect.DeepEqual(prior, c) {
			return nil, ErrInvalid
		}
		contracts[c.ID] = c
	}
	rows := map[string]Candidate{}
	add := func(c Candidate) error {
		channels := p.Kinds[c.Kind]
		if !channels.InApp && !channels.Email && !channels.Push {
			return nil
		}
		c.InApp, c.Email, c.Push = channels.InApp, channels.Email, channels.Push
		if prior, ok := rows[c.SourceKey]; ok && !reflect.DeepEqual(prior, c) {
			return ErrInvalid
		}
		rows[c.SourceKey] = c
		return nil
	}
	scheduled := func(date time.Time) time.Time {
		value := time.Date(date.Year(), date.Month(), date.Day(), p.SendHour, 0, 0, 0, japan)
		if value.Before(now) {
			return now
		}
		return value
	}
	addReminder := func(c Contract, kind Kind, due *string) error {
		if due == nil || c.AccessEndDate != nil && *due > *c.AccessEndDate {
			return nil
		}
		date, _ := time.ParseInLocation("2006-01-02", *due, japan)
		if date.Before(midnight) || date.After(end) {
			return nil
		}
		dateSource := c.NextDateSource
		if kind == TrialEnd {
			dateSource = "explicit_trial_end"
		}
		return add(Candidate{Kind: kind, SourceKey: occurrenceKey(kind, c.ID, *due), ContractID: reference(c.ID), Service: c.Service, DueDate: copyText(due), ScheduledAt: scheduled(date.AddDate(0, 0, -p.LeadDays)), DateSource: dateSource, Amount: copyText(c.Amount), Currency: copyText(c.Currency)})
	}
	for _, c := range contracts {
		if !reminderEligible(c, today) {
			continue
		}
		if e = addReminder(c, TrialEnd, c.TrialEndDate); e != nil {
			return nil, e
		}
		if c.ContractKind == "fixed_term" {
			continue
		}
		if c.NextBillingDate == nil {
			continue
		}
		kind := Renewal
		if annual(c) {
			kind = AnnualCharge
		}
		stopped := c.AutoRenew != nil && !*c.AutoRenew
		if stopped && (kind == Renewal || c.NextDateSource != "manual") {
			continue
		}
		if e = addReminder(c, kind, c.NextBillingDate); e != nil {
			return nil, e
		}
	}
	for _, price := range in.PriceChanges {
		id, e := normalizeID(price.ContractID)
		if e != nil {
			return nil, e
		}
		result, e := normalizeID(price.ResultID)
		if e != nil || price.EventIndex < 0 || price.EventIndex > 31 {
			return nil, ErrInvalid
		}
		if !validMoney(price.OldAmount, price.OldCurrency) || !validMoney(price.NewAmount, price.NewCurrency) {
			return nil, ErrInvalid
		}
		c, exists := contracts[id]
		if !exists {
			return nil, ErrInvalid
		}
		// The comparison target remains tentative, but its current registered
		// price is authoritative. Never label a caller's stale baseline current.
		if !sameNativeAmount(c.Amount, price.OldAmount) || !sameNativeCurrency(c.Currency, price.OldCurrency) {
			return nil, ErrInvalid
		}
		increased, e := NativePriceIncrease(c.Amount, c.Currency, price.NewAmount, price.NewCurrency)
		if e != nil {
			return nil, e
		}
		if !price.Pending || !increased || !reminderEligible(c, today) {
			continue
		}
		e = add(Candidate{Kind: PriceIncrease, SourceKey: priceKey(id, result, price.EventIndex), ContractID: reference(id), ResultID: reference(result), EventIndex: reference(price.EventIndex), Service: c.Service, ScheduledAt: scheduled(midnight), Currency: copyText(c.Currency), OldAmount: copyText(c.Amount), NewAmount: copyText(price.NewAmount)})
		if e != nil {
			return nil, e
		}
	}
	for _, episode := range in.ImportEpisodes {
		account, e := normalizeID(episode.AccountID)
		if e != nil {
			return nil, e
		}
		id, e := normalizeID(episode.EpisodeID)
		if e != nil || !reasonCode.MatchString(episode.Reason) {
			return nil, ErrInvalid
		}
		if !episode.Active || episode.ManualStop {
			continue
		}
		if e = add(Candidate{Kind: ImportError, SourceKey: episodeKey(account, id), AccountID: reference(account), EpisodeID: reference(id), ScheduledAt: scheduled(midnight)}); e != nil {
			return nil, e
		}
	}
	out := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b Candidate) int {
		if v := a.ScheduledAt.Compare(b.ScheduledAt); v != 0 {
			return v
		}
		if a.SourceKey < b.SourceKey {
			return -1
		}
		if a.SourceKey > b.SourceKey {
			return 1
		}
		return 0
	})
	return out, nil
}
