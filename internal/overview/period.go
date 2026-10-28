package overview

import "time"

func Period(now time.Time) (Window, error) {
	today := now.In(time.FixedZone("Asia/Tokyo", 9*3600))
	end := today.AddDate(0, 0, 30)
	if today.Year() < 1 || end.Year() > 9999 {
		return Window{}, ErrUnavailable
	}
	return Window{AsOf: today.Format("2006-01-02"), WindowEnd: end.Format("2006-01-02"), Timezone: "Asia/Tokyo", Month: today.Format("2006-01"), GeneratedAt: now.UTC()}, nil
}
