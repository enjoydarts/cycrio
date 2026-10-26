package billingschedule

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHistoryFilterAndBasisValidation(t *testing.T) {
	for _, f := range []HistoryFilter{{"2024-02-30", "JPY"}, {"2024-01-01", "jpy"}, {"2024-01-01", "JP"}, {"", "JPY"}} {
		if ValidHistoryFilter(f) {
			t.Fatal("invalid filter", f)
		}
	}
	f := HistoryFilter{"0001-01-01", "JPY"}
	if !ValidHistoryFilter(f) {
		t.Fatal(f)
	}
	for _, basis := range []HistoryBasis{{f, "", HistoryAlgorithmVersion}, {f, strings.Repeat("g", 64), HistoryAlgorithmVersion}, {f, strings.Repeat("a", 64), "calendar-v1"}} {
		if ValidHistoryBasis(basis) {
			t.Fatal("invalid basis", basis)
		}
	}
	if !ValidHistoryBasis(HistoryBasis{f, strings.Repeat("a", 64), HistoryAlgorithmVersion}) {
		t.Fatal("valid basis rejected")
	}
}

func TestInferHistoryGregorianCycleAndIntervalBounds(t *testing.T) {
	for _, tt := range []struct {
		dates []string
		want  []Rule
	}{
		{[]string{"2023-07-01", "2023-08-01", "2023-09-01"}, []Rule{{"day", 31, "2023-07-01", false}, {"month", 1, "2023-07-01", false}}},
		{[]string{"2020-02-29", "2021-02-28", "2022-02-28", "2023-02-28", "2024-02-29"}, []Rule{{"year", 1, "2020-02-29", true}}},
		{[]string{"1996-02-29", "2000-02-29", "2004-02-29"}, []Rule{{"day", 1461, "1996-02-29", false}, {"year", 4, "1996-02-29", true}}},
		{[]string{"2096-02-29", "2100-02-28", "2104-02-29"}, []Rule{{"year", 4, "2096-02-29", true}}},
		{[]string{"0001-01-31", "0834-05-31", "1667-09-30"}, []Rule{{"month", 10000, "0001-01-31", true}}},
		{[]string{"0001-01-31", "5000-01-31", "9999-01-31"}, []Rule{{"year", 4999, "0001-01-31", true}}},
	} {
		got, e := InferHistory(tt.dates)
		if e != nil || !reflect.DeepEqual(got, tt.want) {
			t.Fatal(tt.dates, got, e, "want", tt.want)
		}
	}
	a, _ := ParseDate("0001-01-01")
	for _, gap := range []int{10000, 10001} {
		dates := []string{a.Format(time.DateOnly), a.AddDate(0, 0, gap).Format(time.DateOnly), a.AddDate(0, 0, 2*gap).Format(time.DateOnly)}
		got, e := InferHistory(dates)
		if e != nil || gap == 10000 && (len(got) != 1 || got[0].IntervalCount != gap) || gap == 10001 && len(got) != 0 {
			t.Fatal(gap, got, e)
		}
	}
	dates := []string{a.Format(time.DateOnly), a.AddDate(0, 10001, 0).Format(time.DateOnly), a.AddDate(0, 20002, 0).Format(time.DateOnly)}
	if got, e := InferHistory(dates); e != nil || len(got) != 0 {
		t.Fatal("out of interval range", got, e)
	}
	got, e := InferHistory([]string{"9997-12-30", "9998-12-30", "9999-12-30"})
	if e != nil {
		t.Fatal(e)
	}
	for _, rule := range got {
		next, e := Next(rule, "9999-12-31")
		if e != nil || next != nil {
			t.Fatal("out of civil range", rule, next, e)
		}
	}
}

func TestInferHistoryExactCalendarRules(t *testing.T) {
	tests := []struct {
		name  string
		dates []string
		want  []Rule
	}{
		{"month end equivalents", []string{"2023-01-31", "2023-02-28", "2023-03-31"}, []Rule{{"month", 1, "2023-01-31", true}}},
		{"earlier clamped February retained", []string{"2023-02-28", "2023-03-30", "2023-04-30"}, []Rule{{"month", 1, "2023-03-30", false}}},
		{"fixed days", []string{"2024-01-01", "2024-01-29", "2024-02-26"}, []Rule{{"day", 28, "2024-01-01", false}}},
		{"future leap ambiguity", []string{"2021-02-28", "2022-02-28", "2023-02-28"}, []Rule{{"day", 365, "2021-02-28", false}, {"year", 1, "2021-02-28", false}, {"year", 1, "2021-02-28", true}}},
		{"irregular", []string{"2024-01-01", "2024-02-02", "2024-03-01"}, []Rule{}},
		{"insufficient", []string{"2024-01-01", "2024-02-01"}, []Rule{}},
		{"same date", []string{"2024-01-01", "2024-01-01", "2024-02-01"}, []Rule{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InferHistory(tt.dates)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
}

func TestInferHistoryBoundariesAndWholeSample(t *testing.T) {
	for _, dates := range [][]string{
		{"0001-01-01", "0001-01-02", "0001-01-03"},
		{"9999-12-29", "9999-12-30", "9999-12-31"},
		{"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05"},
	} {
		got, err := InferHistory(dates)
		if err != nil || len(got) != 1 || got[0].IntervalCount != 1 || got[0].IntervalUnit != "day" {
			t.Fatal(got, err)
		}
	}
	for _, dates := range [][]string{{"2024-02-30", "2024-03-30", "2024-04-30"}, {"2024-03-01", "2024-02-01", "2024-04-01"}, {"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05", "2024-01-06"}} {
		if _, err := InferHistory(dates); err == nil {
			t.Fatal("invalid input accepted", dates)
		}
	}
	got, err := InferHistory([]string{"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-05"})
	if err != nil || len(got) != 0 {
		t.Fatal("dropped unmatched sample", got, err)
	}
}
