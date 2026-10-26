package billingschedule

import "testing"

func TestCalendar(t *testing.T) {
	for _, tc := range []struct {
		name, unit, anchor, asof, want string
		count                          int
		eom                            bool
	}{
		{"month clamp", "month", "2023-01-31", "2023-02-01", "2023-02-28", 1, false},
		{"original day", "month", "2023-01-31", "2023-03-01", "2023-03-31", 1, false},
		{"thirty", "month", "2023-01-30", "2023-03-01", "2023-03-30", 1, false},
		{"day mode", "month", "2023-02-28", "2023-03-01", "2023-03-28", 1, false},
		{"end mode", "month", "2023-02-28", "2023-03-01", "2023-03-31", 1, true},
		{"leap clamp", "year", "2024-02-29", "2025-01-01", "2025-02-28", 1, false},
		{"leap return", "year", "2024-02-29", "2028-01-01", "2028-02-29", 1, false},
		{"century", "year", "2000-02-29", "2100-01-01", "2100-02-28", 1, false},
		{"multi month", "month", "2024-11-30", "2025-01-01", "2025-02-28", 3, false},
		{"multi year", "year", "2024-02-29", "2025-01-01", "2028-02-29", 4, false},
		{"28 days", "day", "2023-01-31", "2023-03-01", "2023-03-28", 28, false},
		{"week", "week", "2024-01-01", "2024-01-02", "2024-01-15", 2, false},
		{"future", "month", "2026-01-31", "2025-01-01", "2026-01-31", 1, false},
		{"inclusive", "day", "0001-01-01", "9999-12-31", "9999-12-31", 1, false},
		{"max day", "day", "9999-12-30", "9999-12-31", "", 2, false},
		{"max month", "month", "9999-11-30", "9999-12-31", "", 1, false},
		{"max year", "year", "9999-01-01", "9999-12-31", "", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := Next(Rule{IntervalUnit: tc.unit, IntervalCount: tc.count, AnchorDate: tc.anchor, MonthEnd: tc.eom}, tc.asof)
			if e != nil {
				t.Fatal(e)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal(*got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("got %v want %s", got, tc.want)
			}
		})
	}
}
func TestInvalidCalendar(t *testing.T) {
	for _, r := range []Rule{{"month", 1, "0000-01-01", false}, {"month", 1, "2023-02-29", false}, {"month", 1, "2024-1-01", false}, {"month", 0, "2024-01-01", false}, {"month", 10001, "2024-01-01", false}, {"hour", 1, "2024-01-01", false}, {"day", 1, "2024-01-31", true}, {"month", 1, "2024-01-30", true}, {"year", 1, "2024-02-28", true}} {
		if _, e := Normalize(r); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if _, e := Next(Rule{"day", 1, "2024-01-01", false}, "2024-02-30"); e == nil {
		t.Fatal("invalid asof")
	}
}
