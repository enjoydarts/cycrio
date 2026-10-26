package billingschedule

import "time"

// InferHistory compares consecutive occurrences; it never assumes missing payments.
// Callers select the latest eligible dates before invoking this pure comparison.
func InferHistory(dates []string) ([]Rule, error) {
	rules := []Rule{}
	if len(dates) > 5 {
		return nil, ErrInvalid
	}
	ts := make([]time.Time, len(dates))
	duplicate := false
	for i, s := range dates {
		t, e := ParseDate(s)
		if e != nil {
			return nil, e
		}
		ts[i] = t
		if i > 0 {
			if t.Before(ts[i-1]) {
				return nil, ErrInvalid
			}
			duplicate = duplicate || t.Equal(ts[i-1])
		}
	}
	if len(ts) < 3 || duplicate {
		return rules, nil
	}
	gap := (ts[1].Unix() - ts[0].Unix()) / 86400
	fixed := gap >= 1 && gap <= 10000
	step := monthDifference(ts[0], ts[1])
	calendar := step > 0
	anchor := 0
	end := true
	for i, t := range ts {
		if t.Day() > ts[anchor].Day() {
			anchor = i
		}
		end = end && t.Day() == lastDay(t.Year(), t.Month())
		if i > 0 {
			fixed = fixed && (t.Unix()-ts[i-1].Unix())/86400 == gap
			calendar = calendar && monthDifference(ts[i-1], t) == step
		}
	}
	if fixed {
		rules = append(rules, Rule{"day", int(gap), dates[0], false})
	}
	unit, count := "month", step
	if step%12 == 0 {
		unit, count = "year", step/12
	}
	if !calendar || count < 1 || count > 10000 {
		return rules, nil
	}
	dateFits := true
	for _, t := range ts {
		d := ts[anchor].Day()
		if last := lastDay(t.Year(), t.Month()); d > last {
			d = last
		}
		dateFits = dateFits && t.Day() == d
	}
	// Recurring calendar months repeat after 4800/gcd(4800, step) occurrences.
	// Check the full Gregorian cycle, including future leap/century differences.
	equivalent := dateFits && end
	if equivalent {
		for k, n := 0, 4800/historyGCD(4800, step); k < n; k++ {
			t := ts[anchor].AddDate(0, k*step, 1-ts[anchor].Day())
			if lastDay(t.Year(), t.Month()) > ts[anchor].Day() {
				equivalent = false
				break
			}
		}
	}
	if dateFits && !equivalent {
		rules = append(rules, Rule{unit, count, dates[anchor], false})
	}
	if end {
		rules = append(rules, Rule{unit, count, dates[0], true})
	}
	return rules, nil
}

func monthDifference(a, b time.Time) int { return (b.Year()-a.Year())*12 + int(b.Month()-a.Month()) }
func historyGCD(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
