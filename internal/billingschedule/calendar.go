package billingschedule

import "time"

func lastDay(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// Next derives each occurrence from the original anchor. It never chains clamped dates.
// Unix seconds, rather than time.Duration, keep the full civil date range safe.
func Next(rule Rule, asOf string) (*string, error) {
	r, e := Normalize(rule)
	if e != nil {
		return nil, e
	}
	as, e := ParseDate(asOf)
	if e != nil {
		return nil, e
	}
	a, _ := ParseDate(r.AnchorDate)
	if !as.After(a) {
		s := r.AnchorDate
		return &s, nil
	}
	var next time.Time
	if r.IntervalUnit == "day" || r.IntervalUnit == "week" {
		step := int64(r.IntervalCount)
		if r.IntervalUnit == "week" {
			step *= 7
		}
		delta := (as.Unix() - a.Unix()) / 86400
		k := (delta + step - 1) / step
		next = time.Unix(a.Unix()+k*step*86400, 0).UTC()
	} else {
		step := r.IntervalCount
		if r.IntervalUnit == "year" {
			step *= 12
		}
		months := (as.Year()-a.Year())*12 + int(as.Month()-a.Month())
		k := months / step
		for {
			total := (a.Year()-1)*12 + int(a.Month()-1) + k*step
			y, m := total/12+1, time.Month(total%12+1)
			if y > 9999 {
				return nil, nil
			}
			day := a.Day()
			last := lastDay(y, m)
			if r.MonthEnd || day > last {
				day = last
			}
			next = time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
			if !next.Before(as) {
				break
			}
			k++
		}
	}
	if next.Year() > 9999 {
		return nil, nil
	}
	s := next.Format("2006-01-02")
	return &s, nil
}
