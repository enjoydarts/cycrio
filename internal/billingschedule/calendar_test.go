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

func TestCalendarLongRangeAndMaximumCount(t *testing.T) {
	for _, tc := range []struct {
		name, unit, anchor, asof, want string
		count                          int
	}{
		{"day full range", "day", "0001-01-01", "9999-12-31", "9999-12-31", 1},
		{"week full range", "week", "0001-01-01", "9999-12-25", "9999-12-27", 1},
		{"month full range", "month", "0001-01-31", "9999-12-01", "9999-12-31", 1},
		{"year full range", "year", "0001-12-31", "9999-12-01", "9999-12-31", 1},
		{"day full range overflow", "day", "0001-01-02", "9999-12-31", "", 2},
		{"week full range overflow", "week", "0001-01-01", "9999-12-31", "", 1},
		{"month full range overflow", "month", "0001-01-30", "9999-12-31", "", 1},
		{"year full range overflow", "year", "0001-01-01", "9999-12-31", "", 1},
		{"maximum day count", "day", "0001-01-01", "0001-01-02", "0028-05-19", 10000},
		{"maximum week count", "week", "0001-01-01", "0001-01-02", "0192-08-27", 10000},
		{"maximum month count", "month", "0001-01-31", "0001-02-01", "0834-05-31", 10000},
		{"maximum year count overflow", "year", "0001-01-01", "0001-01-02", "", 10000},
		{"maximum day count full range overflow", "day", "0001-01-01", "9999-12-31", "", 10000},
		{"maximum week count full range overflow", "week", "0001-01-01", "9999-12-31", "", 10000},
		{"maximum month count full range overflow", "month", "0001-01-31", "9999-12-31", "", 10000},
		{"maximum year count full range overflow", "year", "0001-01-01", "9999-12-31", "", 10000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Next(Rule{IntervalUnit: tc.unit, IntervalCount: tc.count, AnchorDate: tc.anchor}, tc.asof)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatalf("got %s, want out of range", *got)
				}
			} else if got == nil {
				t.Fatalf("got nil, want %s", tc.want)
			} else if *got != tc.want {
				t.Fatalf("got %s, want %s", *got, tc.want)
			}
		})
	}
}

func TestCalendarMaximumCountStillIncludesOriginalAndFutureAnchor(t *testing.T) {
	for _, unit := range []string{"day", "week", "month", "year"} {
		for _, asof := range []string{"0001-01-01", "9999-12-31"} {
			t.Run(unit+"/"+asof, func(t *testing.T) {
				rule := Rule{IntervalUnit: unit, IntervalCount: 10000, AnchorDate: "9999-12-31"}
				normalized, err := Normalize(rule)
				if err != nil || normalized != rule {
					t.Fatalf("maximum count rejected: %+v, %v", normalized, err)
				}
				got, err := Next(rule, asof)
				if err != nil || got == nil || *got != rule.AnchorDate {
					t.Fatalf("got %v, %v, want original anchor %s", got, err, rule.AnchorDate)
				}
			})
		}
	}
}

func TestCalendarRejectsCountAboveMaximumForEveryUnit(t *testing.T) {
	for _, unit := range []string{"day", "week", "month", "year"} {
		t.Run(unit, func(t *testing.T) {
			rule := Rule{IntervalUnit: unit, IntervalCount: 10001, AnchorDate: "0001-01-01"}
			if _, err := Normalize(rule); err == nil {
				t.Fatal("Normalize accepted count 10001")
			}
			if _, err := Next(rule, "0001-01-01"); err == nil {
				t.Fatal("Next accepted count 10001")
			}
		})
	}
}
