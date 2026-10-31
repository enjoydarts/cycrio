package overview

import (
	"testing"
	"time"
)

func TestPeriodUsesOneTokyoDayAndThirtyCalendarDays(t *testing.T) {
	for _, test := range []struct{ now, day, end string }{
		{"2024-02-29T14:59:59Z", "2024-02-29", "2024-03-30"},
		{"2024-02-29T15:00:00Z", "2024-03-01", "2024-03-31"},
		{"2026-12-31T15:00:00Z", "2027-01-01", "2027-01-31"},
	} {
		now, _ := time.Parse(time.RFC3339, test.now)
		period, err := Period(now)
		if err != nil || period.AsOf != test.day || period.WindowEnd != test.end || period.Timezone != "Asia/Tokyo" || !period.GeneratedAt.Equal(now) {
			t.Fatal("incorrect overview period", period, err)
		}
	}
	if _, err := Period(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("unrepresentable future window accepted")
	}
}
