package mailautomation

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestDefaultScopeUsesJapanCalendarYearAndClampsLeapDay(t *testing.T) {
	for _, tc := range []struct{ now, since, before string }{
		{"2024-02-28T16:00:00Z", "2023-02-28", "2024-03-01"},
		{"2026-10-27T14:59:59Z", "2025-10-27", "2026-10-28"},
		{"2026-10-27T15:00:00Z", "2025-10-28", "2026-10-29"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		s := DefaultScope(now)
		if s.Since != tc.since || s.Before != tc.before || s.Timezone != "Asia/Tokyo" || !s.FullEmail || s.IntervalSeconds != 300 {
			t.Fatalf("%s: %+v", tc.now, s)
		}
	}
}

func TestScopeWindowsStayFixedAndUseJapanInstants(t *testing.T) {
	scope := Scope{Since: "2024-01-01", Before: "2026-01-01", FullEmail: true, IntervalSeconds: 300}
	windows, err := Windows(scope)
	if err != nil || len(windows) != 2 || windows[0].Since != "2024-01-01" || windows[0].Before != "2025-01-01" || windows[1].Before != "2026-01-01" {
		t.Fatal(windows, err)
	}
	a, b, err := Bounds(scope)
	if err != nil || a.UTC().Format(time.RFC3339) != "2023-12-31T15:00:00Z" || b.UTC().Format(time.RFC3339) != "2025-12-31T15:00:00Z" {
		t.Fatal(a, b, err)
	}
}

func TestActivationRequiresWholeEmailExplicitConsentAndSafeVersions(t *testing.T) {
	in := Input{ExpectedAccountVersion: 1, RuntimeVersion: 2, ProfileID: uuid.NewString(), PolicyVersion: 1, Since: "2025-01-01", Before: "2026-01-01", FullEmail: true, IntervalSeconds: 300, Confirmed: true, RequestID: uuid.NewString()}
	if !ValidInput(in) {
		t.Fatal("valid rejected")
	}
	for _, mutate := range []func(*Input){
		func(x *Input) { x.Confirmed = false }, func(x *Input) { x.FullEmail = false }, func(x *Input) { x.Since = "2025-02-29" }, func(x *Input) { x.ExpectedSettingsVersion = -1 }, func(x *Input) { x.RuntimeVersion = 9007199254740992 }, func(x *Input) { x.IntervalSeconds = 0 },
	} {
		bad := in
		mutate(&bad)
		if ValidInput(bad) {
			t.Fatalf("invalid accepted: %+v", bad)
		}
	}
}

func TestUnpaidDiscoveryBackoffHasFiveBoundedRetries(t *testing.T) {
	for failure, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second} {
		got, retry := DiscoveryBackoff(failure + 1)
		if !retry || got != want {
			t.Fatal(failure+1, got, retry)
		}
	}
	for _, failure := range []int{0, -1, 6, 100} {
		if _, retry := DiscoveryBackoff(failure); retry {
			t.Fatal("unbounded retry", failure)
		}
	}
}
