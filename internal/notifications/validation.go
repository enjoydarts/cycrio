package notifications

import (
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var nativeAmount = regexp.MustCompile(`^[0-9]{1,24}(\.[0-9]{1,12})?$`)
var nativeCurrency = regexp.MustCompile(`^[A-Z]{3}$`)
var reasonCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var japan = time.FixedZone(Timezone, 9*60*60)

func DefaultPreferences() Preferences {
	p := Preferences{Version: 1, Kinds: map[Kind]Channels{}, LeadDays: 7, SendHour: 9, Timezone: Timezone}
	for _, kind := range kinds {
		p.Kinds[kind] = Channels{InApp: true}
	}
	return p
}
func NormalizePreferences(p Preferences) (Preferences, error) {
	if p.Version < 1 || p.LeadDays < 0 || p.LeadDays > 30 || p.SendHour < 0 || p.SendHour > 23 || p.Timezone != Timezone || len(p.Kinds) != len(kinds) {
		return Preferences{}, ErrInvalid
	}
	normalized := p
	normalized.Kinds = map[Kind]Channels{}
	for _, kind := range kinds {
		channels, ok := p.Kinds[kind]
		if !ok || channels.Email && !p.EmailOptIn || channels.Push && !p.PushOptIn {
			return Preferences{}, ErrInvalid
		}
		normalized.Kinds[kind] = channels
	}
	return normalized, nil
}
func normalizeID(id string) (string, error) {
	if len(id) != 36 {
		return "", ErrInvalid
	}
	v, e := uuid.Parse(id)
	if e != nil || v == uuid.Nil {
		return "", ErrInvalid
	}
	return v.String(), nil
}
func label(v string) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= 1 && utf8.RuneCountInString(v) <= 200 && strings.TrimSpace(v) != "" && !strings.ContainsFunc(v, unicode.IsControl)
}
func validDate(v *string) bool {
	if v == nil {
		return true
	}
	d, e := time.Parse("2006-01-02", *v)
	return e == nil && d.Year() >= 1 && d.Format("2006-01-02") == *v
}
func validMoney(amount, currency *string) bool {
	return (amount == nil || nativeAmount.MatchString(*amount)) && (currency == nil || nativeCurrency.MatchString(*currency))
}

func sameNativeAmount(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, leftOK := new(big.Rat).SetString(*a)
	right, rightOK := new(big.Rat).SetString(*b)
	return leftOK && rightOK && left.Cmp(right) == 0
}

func sameNativeCurrency(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func normalizeContract(c Contract) (Contract, error) {
	var e error
	c.ID, e = normalizeID(c.ID)
	if e != nil {
		return c, e
	}
	if !label(c.Service) || !slices.Contains([]string{"active", "unknown", "ended"}, c.Status) || !slices.Contains([]string{"unknown", "recurring", "fixed_term"}, c.ContractKind) || !validMoney(c.Amount, c.Currency) {
		return c, ErrInvalid
	}
	for _, date := range []*string{c.NextBillingDate, c.TrialEndDate, c.AccessEndDate} {
		if !validDate(date) {
			return c, ErrInvalid
		}
	}
	if c.NextDateSource != "" && !slices.Contains([]string{"manual", "receipt_estimate"}, c.NextDateSource) || c.NextBillingDate != nil && c.NextDateSource == "" {
		return c, ErrInvalid
	}
	if (c.IntervalUnit == nil) != (c.IntervalCount == nil) || c.IntervalUnit != nil && (!slices.Contains([]string{"day", "week", "month", "year"}, *c.IntervalUnit) || *c.IntervalCount < 1 || *c.IntervalCount > 10000) {
		return c, ErrInvalid
	}
	return c, nil
}

// NativePriceIncrease compares only known regular prices in one native currency.
// Unknown facts and currency changes never become zero values or FX estimates.
func NativePriceIncrease(oldAmount, oldCurrency, newAmount, newCurrency *string) (bool, error) {
	if !validMoney(oldAmount, oldCurrency) || !validMoney(newAmount, newCurrency) {
		return false, ErrInvalid
	}
	if oldAmount == nil || oldCurrency == nil || newAmount == nil || newCurrency == nil || *oldCurrency != *newCurrency {
		return false, nil
	}
	old, _ := new(big.Rat).SetString(*oldAmount)
	next, _ := new(big.Rat).SetString(*newAmount)
	return next.Cmp(old) > 0, nil
}
