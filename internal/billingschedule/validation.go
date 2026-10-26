package billingschedule

import (
	"time"

	"github.com/enjoydarts/cycrio/internal/subscriptions"
)

func ParseDate(s string) (time.Time, error) {
	t, e := time.Parse("2006-01-02", s)
	if e != nil || len(s) != 10 || t.Year() < 1 || t.Year() > 9999 || t.Format("2006-01-02") != s {
		return time.Time{}, ErrInvalid
	}
	return t, nil
}
func Normalize(r Rule) (Rule, error) {
	a, e := ParseDate(r.AnchorDate)
	if e != nil || r.IntervalCount < 1 || r.IntervalCount > 10000 {
		return Rule{}, ErrInvalid
	}
	switch r.IntervalUnit {
	case "day", "week", "month", "year":
	default:
		return Rule{}, ErrInvalid
	}
	if r.MonthEnd && (r.IntervalUnit != "month" && r.IntervalUnit != "year" || a.Day() != lastDay(a.Year(), a.Month())) {
		return Rule{}, ErrInvalid
	}
	return r, nil
}
func NormalizeSave(in SaveInput) (SaveInput, error) {
	if _, e := Normalize(in.Rule); e != nil {
		return SaveInput{}, e
	}
	if !subscriptions.ValidID(in.RequestID) || in.ExpectedVersion < 1 || !in.Confirmed {
		return SaveInput{}, ErrInvalid
	}
	if _, e := ParseDate(in.ExpectedAsOf); e != nil {
		return SaveInput{}, e
	}
	if _, e := ParseDate(in.ExpectedCandidate); e != nil {
		return SaveInput{}, e
	}
	return in, nil
}
