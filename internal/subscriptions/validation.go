package subscriptions

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var decimal = regexp.MustCompile(`^[0-9]{1,24}(\.[0-9]{1,12})?$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)

func ValidID(id string) bool { return uuid.Validate(id) == nil && len(id) == 36 }
func validLabel(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && utf8.RuneCountInString(s) >= 1 && utf8.RuneCountInString(s) <= 200
}
func Normalize(v Values) (Values, error) {
	v.Service = strings.TrimSpace(v.Service)
	if !validLabel(v.Service) || !slices.Contains([]string{"active", "ended"}, v.Status) {
		return Values{}, ErrInvalid
	}
	if v.Plan != nil {
		p := strings.TrimSpace(*v.Plan)
		if !validLabel(p) {
			return Values{}, ErrInvalid
		}
		v.Plan = &p
	}
	if (v.Amount == nil) != (v.Currency == nil) || v.Amount != nil && (!decimal.MatchString(*v.Amount) || !currency.MatchString(*v.Currency)) {
		return Values{}, ErrInvalid
	}
	if (v.IntervalUnit == nil) != (v.IntervalCount == nil) || v.IntervalUnit != nil && (!slices.Contains([]string{"day", "week", "month", "year"}, *v.IntervalUnit) || *v.IntervalCount < 1 || *v.IntervalCount > 10000) {
		return Values{}, ErrInvalid
	}
	if v.NextBillingDate != nil {
		d, e := time.Parse("2006-01-02", *v.NextBillingDate)
		if e != nil || d.Format("2006-01-02") != *v.NextBillingDate || d.Year() < 1 {
			return Values{}, ErrInvalid
		}
	}
	return v, nil
}
func ValidSource(id *string, index *int) bool {
	return id == nil && index == nil || id != nil && index != nil && ValidID(*id) && *index >= 0 && *index < 32
}
func NormalizeCreate(in CreateInput) (CreateInput, error) {
	if !in.Confirmed || !ValidID(in.RequestID) || !ValidSource(in.SourceResultID, in.SourceEventIndex) {
		return CreateInput{}, ErrInvalid
	}
	v, e := Normalize(in.Values)
	in.Values = v
	return in, e
}
func NormalizeUpdate(in UpdateInput) (UpdateInput, error) {
	if !in.Confirmed || in.ExpectedVersion < 1 {
		return UpdateInput{}, ErrInvalid
	}
	v, e := Normalize(in.Values)
	in.Values = v
	return in, e
}
func ValidSourceInput(in SourceInput) bool {
	return in.Confirmed && in.ExpectedVersion > 0 && ValidSource(&in.SourceResultID, &in.SourceEventIndex)
}
func InvalidFilter(f Filter) bool {
	return f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || !utf8.ValidString(f.Query) || strings.ContainsRune(f.Query, 0) || utf8.RuneCountInString(f.Query) > 200 || !slices.Contains([]string{"", "all", "active", "ended"}, f.Status) || !slices.Contains([]string{"", "next_billing", "service", "updated"}, f.Sort)
}
func InvalidAnalysisFilter(f AnalysisFilter) bool {
	return f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.AccountID != "" && !ValidID(f.AccountID) || !slices.Contains([]string{"", "all", "related", "failed", "unknown", "not_related"}, f.State) || !slices.Contains([]string{"", "all", "pending", "registered"}, f.Review) || (f.Review == "pending" || f.Review == "registered") && f.State != "related"
}
