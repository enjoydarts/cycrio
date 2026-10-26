package billing

import (
	"regexp"
	"slices"
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

var amountPattern = regexp.MustCompile(`^[0-9]{1,24}(\.[0-9]{1,12})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidID(s string) bool { return subscriptions.ValidID(s) }
func validDate(s string) bool {
	d, e := time.Parse("2006-01-02", s)
	return e == nil && d.Year() > 0 && d.Format("2006-01-02") == s
}
func Normalize(v Values) (Values, error) {
	if !slices.Contains([]string{"invoice", "payment", "refund"}, v.Kind) || !slices.Contains([]string{"active", "voided"}, v.State) || !validDate(v.OccurredOn) || !amountPattern.MatchString(v.Amount) || !currencyPattern.MatchString(v.Currency) || (v.Kind == "refund") != (v.RefundOfID != nil) || v.RefundOfID != nil && !ValidID(*v.RefundOfID) {
		return Values{}, ErrInvalid
	}
	return v, nil
}
func validToken(t *string) bool { return t == nil || tokenPattern.MatchString(*t) }
func NormalizeCreate(in CreateInput) (CreateInput, error) {
	if !in.Confirmed || !ValidID(in.RequestID) || !subscriptions.ValidSource(in.SourceResultID, in.SourceEventIndex) || !validToken(in.DuplicateToken) {
		return CreateInput{}, ErrInvalid
	}
	v, e := Normalize(in.Values)
	in.Values = v
	return in, e
}
func NormalizeUpdate(in UpdateInput) (UpdateInput, error) {
	if !in.Confirmed || in.ExpectedVersion < 1 || !validToken(in.DuplicateToken) {
		return UpdateInput{}, ErrInvalid
	}
	v, e := Normalize(in.Values)
	in.Values = v
	return in, e
}
func ValidSourceInput(in SourceInput) bool { return subscriptions.ValidSourceInput(in) }
func MonthBounds(month string) (string, string, error) {
	d, e := time.Parse("2006-01", month)
	if e != nil || d.Year() < 1 || d.Format("2006-01") != month {
		return "", "", ErrInvalid
	}
	return d.Format("2006-01-02"), d.AddDate(0, 1, 0).Format("2006-01-02"), nil
}
func InvalidFilter(f Filter) bool {
	if f.Month != "" {
		if _, _, e := MonthBounds(f.Month); e != nil {
			return true
		}
	}
	return f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || !slices.Contains([]string{"", "invoice", "payment", "refund"}, f.Kind) || !slices.Contains([]string{"", "active", "voided"}, f.State) || !subscriptions.ValidSource(f.SourceResultID, f.SourceEventIndex)
}
func InvalidDuplicateFilter(f DuplicateFilter) bool {
	return f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || !slices.Contains([]string{"invoice", "payment", "refund"}, f.Kind) || !validDate(f.OccurredOn) || !amountPattern.MatchString(f.Amount) || !currencyPattern.MatchString(f.Currency) || f.ExcludeID != "" && !ValidID(f.ExcludeID)
}
